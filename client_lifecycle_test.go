package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// anchoredFleet answers a moving-tag establishment with one coherent head.
func anchoredFleet(t *testing.T, count int, opts ...ethguard.Option) (*ethguard.Client, []*rpctest.Node) {
	t.Helper()
	client, nodes := fleet(t, count, opts...)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			var ref string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &ref)
			}
			var full bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &full)
			}
			if full || ref != "latest" {
				return nil, rpctest.RPCError(-32602, "unexpected anchor probe "+ref)
			}
			return map[string]any{"number": "0x10", "hash": "0x1111111111111111111111111111111111111111111111111111111111111111"}, nil
		})
	}
	return client, nodes
}

// TestAnchorOperationPublishesCompletionOnce locks the standalone anchor's
// delivery contract: one completion, the query's own decision, and the
// material snapshot when evidence collection is configured.
func TestAnchorOperationPublishesCompletionOnce(t *testing.T) {
	client, _ := anchoredFleet(t, 3)
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := ethguard.WithResult(base)

	hash, _, err := client.AnchorBlockHash(ctx)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if hash == "" {
		t.Fatal("empty anchor hash")
	}
	result, published, err := capture.Result()
	if !published || err != nil {
		t.Fatalf("capture delivery = %v, %v", published, err)
	}
	if result.Err != nil {
		t.Fatalf("completion error = %v", result.Err)
	}
	if !result.IsCorroborated() {
		t.Fatalf("anchor decision = %+v, want the query's own corroboration", result.Decision)
	}
	if result.Decision.Reference == nil || result.Decision.Reference.Status.String() != "corroborated" || result.Decision.Reference.Hash != hash {
		t.Fatalf("reference evidence = %+v, want the established hash %s", result.Decision.Reference, hash)
	}
	if result.Uncertain == nil || len(result.Uncertain.Raw) == 0 {
		t.Fatalf("configured evidence was not delivered: %+v", result.Uncertain)
	}
	// A second read returns the same completed delivery — never a fresh
	// publication or a fake unfinished state.
	again, publishedAgain, err := capture.Result()
	if !publishedAgain || err != nil || again.Decision.Reference.Hash != hash {
		t.Fatalf("second read = %+v, %v, %v", again, publishedAgain, err)
	}
}

// TestAnchorOperationLocalRejectionPublishesCompletion locks the publish
// rule for a parameter rejection: the capture was bound before validation,
// the rejection is the completion result, and no upstream was contacted.
func TestAnchorOperationLocalRejectionPublishesCompletion(t *testing.T) {
	client, nodes := anchoredFleet(t, 2)
	ctx, capture := ethguard.WithResult(context.Background())

	hash, _, err := client.AnchorBlockHashForTag(ctx, "pending")
	if hash != "" || !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("pending tag = %q, %v, want a local ErrInvalidRequest", hash, err)
	}
	result, published, err := capture.Result()
	if !published || err != nil {
		t.Fatalf("capture delivery = %v, %v", published, err)
	}
	if !errors.Is(result.Err, ethguard.ErrInvalidRequest) {
		t.Fatalf("completion error = %v", result.Err)
	}
	if result.Decision != nil && result.Decision.Reference != nil {
		t.Fatalf("a pre-adjudication rejection carries no reference: %+v", result.Decision)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			t.Errorf("rejected request reached a provider: %s", call.Method)
		}
	}
}

// TestCloseConvergesInFlightBackgroundRead proves the owner lifecycle
// cancels library-owned network work: a call whose caller passed
// context.Background still returns promptly when the client closes, with a
// recognizable ErrPoolClosed cause.
func TestCloseConvergesInFlightBackgroundRead(t *testing.T) {
	client, nodes := anchoredFleet(t, 2)
	started := make(chan struct{})
	var once sync.Once
	blocker := make(chan struct{})
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			once.Do(func() { close(started) })
			<-blocker
			return map[string]any{"number": "0x10", "hash": "0x1111111111111111111111111111111111111111111111111111111111111111"}, nil
		})
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := client.AnchorBlockHash(context.Background())
		done <- err
	}()
	<-started
	begin := time.Now()
	client.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ethguard.ErrPoolClosed) {
			t.Fatalf("in-flight close error = %v, want ErrPoolClosed", err)
		}
		if elapsed := time.Since(begin); elapsed > 5*time.Second {
			t.Fatalf("close convergence took %s", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight call did not converge on owner close")
	}
	// Unblock the scripted handlers so the fleet shuts down cleanly.
	close(blocker)
}

// TestClosedClientRejectsBeforeNetwork locks the new-call gate: zero
// network contact, ErrPoolClosed, and the completion still published to a
// bound capture.
func TestClosedClientRejectsBeforeNetwork(t *testing.T) {
	client, nodes := anchoredFleet(t, 2)
	client.Close()

	ctx, capture := ethguard.WithResult(context.Background())
	hash, _, err := client.AnchorBlockHash(ctx)
	if hash != "" || !errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatalf("closed call = %q, %v, want ErrPoolClosed", hash, err)
	}
	result, published, err := capture.Result()
	if !published || err != nil {
		t.Fatalf("closed-gate delivery = %v, %v", published, err)
	}
	if !errors.Is(result.Err, ethguard.ErrPoolClosed) {
		t.Fatalf("completion error = %v", result.Err)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			t.Errorf("closed client contacted %s", call.Method)
		}
	}
}

// TestUserCancellationKeepsItsOwnCause distinguishes the two cancellation
// directions: a user deadline that fires first keeps the context error and
// never masquerades as a client close.
func TestUserCancellationKeepsItsOwnCause(t *testing.T) {
	client, nodes := anchoredFleet(t, 2)
	blocker := make(chan struct{})
	defer close(blocker)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			<-blocker
			return map[string]any{"number": "0x10", "hash": "0x1111111111111111111111111111111111111111111111111111111111111111"}, nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := client.AnchorBlockHash(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("user deadline error = %v, want DeadlineExceeded", err)
	}
	if errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatal("a user deadline must not read as ErrPoolClosed")
	}
}

// TestConcurrentCloseSharesOneCompletion: every concurrent closer returns
// under the same background-task completion conditions.
func TestConcurrentCloseSharesOneCompletion(t *testing.T) {
	client, _ := anchoredFleet(t, 2, ethguard.WithTipTracking(50*time.Millisecond, 8))
	var wg sync.WaitGroup
	errs := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.Close()
			errs <- struct{}{}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Close deadlocked")
	}
	// All three returned; the pool is closed for new work.
	if _, _, err := client.AnchorBlockHash(context.Background()); !errors.Is(err, ethguard.ErrPoolClosed) {
		t.Fatalf("post-close call = %v, want ErrPoolClosed", err)
	}
}

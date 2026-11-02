package pool

import (
	"context"
	"encoding/json"
	"math/big"
	"sync"
	"testing"
	"time"
)

// sharedBudgetPeer answers its first call immediately so the test can warm a
// connection, then blocks every later call in the controlled budget scenario.
// It is a test transport, not a provider compatibility sample.
type sharedBudgetPeer struct {
	mu      sync.Mutex
	calls   int
	warmed  bool
	entered chan string
	release chan struct{}
}

func (p *sharedBudgetPeer) CallContext(ctx context.Context, result any, method string, _ ...any) error {
	p.mu.Lock()
	p.calls++
	warm := !p.warmed
	p.warmed = true
	p.mu.Unlock()

	if warm {
		*result.(*json.RawMessage) = json.RawMessage(`"0x1"`)
		return nil
	}
	p.entered <- method
	select {
	case <-p.release:
		*result.(*json.RawMessage) = json.RawMessage(`"0x1"`)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*sharedBudgetPeer) ChainID(context.Context) (*big.Int, error) { return big.NewInt(1), nil }
func (*sharedBudgetPeer) Close()                                    {}

func (p *sharedBudgetPeer) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func waitForBudgetCall(t *testing.T, entered <-chan string, ctx context.Context) {
	t.Helper()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("shared-budget call never reached the transport")
	}
}

// TestTipProbeAndReferenceConfirmationShareSourceBudget holds both source
// slots with a tip probe and a reference-shaped voting round, then proves a
// third business read waits without sending and without health penalties.
// The probe and the reference round are the two scheduling paths that the
// earlier budget fixture did not put in the same competition.
func TestTipProbeAndReferenceConfirmationShareSourceBudget(t *testing.T) {
	peer := &sharedBudgetPeer{
		entered: make(chan string, 4),
		release: make(chan struct{}),
	}
	p, err := NewWithMembers(1, []Member{{
		Name:   "provider",
		URL:    "https://provider.example",
		Dialer: &contractDialer{transport: peer},
	}}, WithTipTracking(time.Hour, 16))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	source := p.VotingMembers()[0]

	// Warm the connection: tip probes observe established transports only.
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{Sources: []*Source{source}}); err != nil {
		t.Fatalf("warm call: %v", err)
	}

	probeDone := make(chan struct{})
	go func() {
		p.RefreshTips(ctx)
		close(probeDone)
	}()
	waitForBudgetCall(t, peer.entered, ctx)

	referenceDone := make(chan error, 1)
	go func() {
		_, err := p.CollectVoting(ctx, "eth_getBlockByNumber", []any{"latest", false}, CollectOptions{Sources: []*Source{source}})
		referenceDone <- err
	}()
	waitForBudgetCall(t, peer.entered, ctx)

	// Both slots are held. A third read must wait for its deadline, not send.
	waitCtx, stop := context.WithTimeout(ctx, 40*time.Millisecond)
	defer stop()
	emissions, err := p.CollectVoting(waitCtx, "eth_getBlockByNumber", []any{"latest", false}, CollectOptions{Sources: []*Source{source}})
	if err == nil {
		t.Fatal("third read did not wait for the shared source budget")
	}
	if len(emissions) != 1 || emissions[0].Attempted {
		t.Fatalf("queued read disappeared or was sent: %+v", emissions)
	}
	if got := peer.callCount(); got != 3 {
		t.Fatalf("budget wait bypassed the source slots: calls = %d, want 3", got)
	}
	if health := source.Health(); health.Successes != 1 || health.Failures != 0 {
		t.Fatalf("local wait changed health: %+v", health)
	}

	close(peer.release)
	select {
	case <-probeDone:
	case <-ctx.Done():
		t.Fatal("tip probe did not finish after slots were released")
	}
	select {
	case err := <-referenceDone:
		if err != nil {
			t.Fatalf("reference round: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("reference round did not finish after slots were released")
	}

	emissions, err = p.CollectVoting(ctx, "eth_getBlockByNumber", []any{"latest", false}, CollectOptions{Sources: []*Source{source}})
	if err != nil || len(emissions) != 1 || !emissions[0].Attempted || emissions[0].Err != nil {
		t.Fatalf("budget did not recover: %+v, err = %v", emissions, err)
	}
	if got := peer.callCount(); got != 4 {
		t.Fatalf("recovered call count = %d, want 4 (no replay)", got)
	}
	if health := source.Health(); health.Failures != 0 {
		t.Fatalf("shared-budget competition penalized health: %+v", health)
	}
}

package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"ethguard"
	"ethguard/config"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

func commonHeads(nodes []*rpctest.Node, heights []uint64, confirm func(int, uint64) (any, error)) {
	for i, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(call rpctest.Call) (any, error) {
			var tag string
			if err := json.Unmarshal(call.Params[0], &tag); err != nil {
				return nil, err
			}
			if tag == "finalized" {
				return map[string]any{"number": fmt.Sprintf("0x%x", heights[i]), "hash": fmt.Sprintf("0x%064x", heights[i])}, nil
			}
			var height uint64
			if _, err := fmt.Sscanf(tag, "0x%x", &height); err != nil {
				return nil, err
			}
			if confirm != nil {
				return confirm(i, height)
			}
			return map[string]any{"number": tag, "hash": fmt.Sprintf("0x%064x", height)}, nil
		})
		node.Return(methods.MethodGetBalance, "0x1")
	}
}

func TestCommonReferenceConfirmsDifferentHeightsAndKeepsEvidenceScoped(t *testing.T) {
	events := make(decisionEvents, 8)
	opts := []ethguard.Option{ethguard.WithFinalizedAnchoring(ethguard.WithMaxFinalizedLag(16)), ethguard.WithRecorder(events)}
	client, nodes := fleet(t, 1, 1, 3, opts...)
	commonHeads(nodes, []uint64{84, 100, 100}, nil)
	decision, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if err != nil {
		t.Fatal(err)
	}
	wantHeight, wantSupport, wantCalls := uint64(100), 2, 3
	ref := decision.Reference
	if ref == nil || ref.Height != wantHeight || ref.Status != rpc.Corroborated || len(ref.Support) != wantSupport || len(ref.Participants) != 3 {
		t.Fatalf("unexpected reference: %+v", ref)
	}
	if decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
		t.Fatalf("unexpected answer: %+v", decision)
	}
	if got := anchorEstablishments(nodes); got != wantCalls {
		t.Fatalf("head calls=%d want=%d", got, wantCalls)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBalance {
				var pin string
				json.Unmarshal(call.Params[1], &pin)
				if pin != ref.Hash {
					t.Fatalf("business read escaped pin: %s", pin)
				}
			}
		}
	}
	anchors := 0
	for len(events) > 0 {
		event := <-events
		if event.Method == "anchor" {
			anchors++
			if event.Status != "corroborated" || len(event.Attempts) != wantCalls {
				t.Fatalf("incomplete establishment event: %+v", event)
			}
		}
	}
	if anchors != 1 {
		t.Fatalf("anchor events=%d", anchors)
	}
	observed := ref.ObservedAt
	ref.Support[0].Name = "caller mutation"
	again, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if again.Reference.Support[0].Name == "caller mutation" || again.Reference.ObservedAt.Equal(observed) || anchorEstablishments(nodes) <= wantCalls {
		t.Fatalf("cross-call cache was retained or evidence was exposed: %+v", again.Reference)
	}
}

func TestCommonReferenceConfirmationCannotShrinkQuorum(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 5, ethguard.WithFinalizedAnchoring())
	commonHeads(nodes, []uint64{84, 100, 101, 102, 103}, func(i int, height uint64) (any, error) {
		if i == 4 {
			return nil, rpctest.RPCError(-32603, "confirmation unavailable")
		}
		return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": fmt.Sprintf("0x%064x", height)}, nil
	})
	decision, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("two of five must fail, got %+v %v", decision, err)
	}
	if decision.Reference == nil || len(decision.Reference.Participants) != 5 || len(decision.Reference.Support) != 2 || len(decision.Reference.Faults) == 0 {
		t.Fatalf("lost failed reference evidence: %+v", decision.Reference)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBalance {
				t.Fatal("business read issued after insufficient confirmation")
			}
		}
	}
}

func TestCommonReferenceChoosesHighestFinalizedHeightCoveredByQuorum(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 4, ethguard.WithFinalizedAnchoring())
	commonHeads(nodes, []uint64{10000, 9999, 10001, 9999}, nil)
	decision, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reference == nil || decision.Reference.Height != 9999 || len(decision.Reference.Support) != 4 {
		t.Fatalf("expected highest Q=3-covered finalized height 9999, got %+v", decision.Reference)
	}
}

func TestCommonReferenceDoesNotPromoteUndercoveredHigherFinalizedHeight(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 5, ethguard.WithFinalizedAnchoring())
	commonHeads(nodes, []uint64{10000, 10000, 9999, 9999, 9999}, nil)
	decision, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reference == nil || decision.Reference.Height != 9999 {
		t.Fatalf("undercovered height 10000 was promoted: %+v", decision.Reference)
	}
}

func TestCommonReferenceRejectsWrongConfirmationHeight(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithFinalizedAnchoring())
	commonHeads(nodes, []uint64{84, 100, 101}, func(int, uint64) (any, error) {
		return map[string]any{"number": "0x63", "hash": fmt.Sprintf("0x%064x", 99)}, nil
	})
	d, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if !errors.Is(err, rpc.ErrConflict) || d.Reference == nil || len(d.Reference.Faults) == 0 {
		t.Fatalf("wrong height confirmations accepted: %+v %v", d, err)
	}
}

func TestBuiltInAnchorsHonorCollectTimeout(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		t.Run(fmt.Sprint(finalized), func(t *testing.T) {
			opts := []ethguard.Option{ethguard.WithCollectTimeout(20 * time.Millisecond)}
			if finalized {
				opts = append(opts, ethguard.WithFinalizedAnchoring())
			}
			client, nodes := fleet(t, 1, 1, 2, opts...)
			for _, node := range nodes {
				node.Hang(methods.MethodGetBlockByNumber, 2*time.Second)
			}
			start := time.Now()
			_, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected collection timeout, got %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("anchor ignored configured collect timeout")
			}
		})
	}
}

type waitingAnchor struct{ started, release chan struct{} }

func (a *waitingAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	close(a.started)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-a.release:
		return "0xabc", nil
	}
}
func TestCachedAnchorWaiterCancelsIndependently(t *testing.T) {
	inner := &waitingAnchor{make(chan struct{}), make(chan struct{})}
	cached := ethguard.NewCachedAnchor(inner, time.Minute)
	leader := make(chan error, 1)
	go func() { _, err := cached.AnchorBlockHash(context.Background()); leader <- err }()
	<-inner.started
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() { _, err := cached.AnchorBlockHash(ctx); waiter <- err }()
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiter returned %v", err)
		}
	case <-time.After(time.Second):
		t.Error("waiter ignored cancellation")
	}
	close(inner.release)
	if err := <-leader; err != nil {
		t.Fatal(err)
	}
	if _, err := cached.AnchorBlockHash(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cache hit returned %v", err)
	}
}

func TestCachedLegacyCustomReferenceStillSupportsHashReads(t *testing.T) {
	inner := ethguard.NewCachedAnchor(hashOnlyAnchor{}, time.Minute)
	_, err := ethguard.New(config.Config{Upstreams: []config.Upstream{{ID: "a", Endpoint: "http://a.example"}}}, ethguard.WithAnchorResolver(inner))
	if err == nil {
		t.Fatal("expected custom resolver path to be retired")
	}
}

type cancellingAnchor struct {
	cancel context.CancelFunc
	calls  int
}

func (a *cancellingAnchor) AnchorBlockHash(context.Context) (string, error) {
	a.calls++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	return "0xabc", nil
}
func TestCachedAnchorDoesNotCacheCancelledLeader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &cancellingAnchor{cancel: cancel}
	cached := ethguard.NewCachedAnchor(inner, time.Minute)
	if _, err := cached.AnchorBlockHash(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled leader succeeded: %v", err)
	}
	if _, err := cached.AnchorBlockHash(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("cancelled establishment was cached: calls=%d", inner.calls)
	}
}
func TestFailedStrictAnchorRetainsParticipants(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 2, ethguard.WithCollectTimeout(20*time.Millisecond))
	for _, node := range nodes {
		node.Hang(methods.MethodGetBlockByNumber, time.Second)
	}
	d, err := client.Call(context.Background(), methods.MethodGetBalance, "0x1", "latest")
	if err == nil || d.Reference == nil || len(d.Reference.Participants) != 2 {
		t.Fatalf("failed anchor lost membership: %+v %v", d.Reference, err)
	}
}

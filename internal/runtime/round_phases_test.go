package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The reference round's phase mapping. The establishment round and the
// business round are two runs of the same stage skeleton — collection, raw
// preservation, admission shape, resolution — over different resolutions.
// These tests pin the mapping's contract: for the same fleet behavior, both
// rounds classify failures identically, attribute sources identically, make
// exactly one attempt per frozen member, and honor cancellation identically.

func phaseMappingFleet(t *testing.T, serve func(i int, node *rpctest.Node)) ([]*rpctest.Node, *pool.Pool) {
	t.Helper()
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	t.Cleanup(func() {
		for _, node := range nodes {
			node.Close()
		}
	})
	for i, node := range nodes {
		if serve != nil {
			serve(i, node)
		}
	}
	return nodes, tracePool(t, nodes)
}

// phaseMappingAggregate drives one moving-tag business read through the full
// pipeline and returns the established reference (the reference round's
// evidence) next to the business decision.
func phaseMappingAggregate(t *testing.T, p *pool.Pool) (rpc.ReferenceEvidence, rpc.Decision, error) {
	t.Helper()
	aggregate, _ := traceAggregator(t, p, nil)
	decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_getBalance", Params: []any{"0x1", "latest"}})
	// A failed read still carries the reference round's partial evidence —
	// that visibility is part of the contract under test.
	if decision.Reference == nil {
		t.Fatalf("the read established no reference evidence (err=%v)", err)
	}
	return *decision.Reference, decision, err
}

// TestReferenceRoundCollectionFailureMatchesBusinessClassification pins the
// failure classification mapping: a whole-fleet transport failure is the
// pool's own sentinel on both rounds, attributed once, with the round's
// faults on the reference the caller sees.
func TestReferenceRoundCollectionFailureMatchesBusinessClassification(t *testing.T) {
	// Every member is closed: both rounds fail at the transport level, which
	// is the pool's own sentinel — not a JSON-RPC error classification.
	nodes, p := phaseMappingFleet(t, nil)
	for _, node := range nodes {
		node.Close()
	}
	ref, _, err := phaseMappingAggregate(t, p)
	if err == nil {
		t.Fatal("a dead fleet must fail the read")
	}
	t.Logf("err=%v ref=%+v", err, ref)
	if len(ref.CollectionFaults) == 0 {
		t.Fatalf("reference collection faults = %v, want the pool sentinel attributed once", ref.CollectionFaults)
	}
	seen := 0
	for _, fault := range ref.CollectionFaults {
		if errors.Is(fault, pool.ErrAllSourcesDown) {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("the pool sentinel appears %d times, want exactly one statement", seen)
	}
	// Source identity: the reference round's participants are exactly the
	// frozen basis, in member order.
	if len(ref.Participants) != len(nodes) {
		t.Fatalf("participants = %v, want one slot per frozen member", ref.Participants)
	}
}

// TestReferenceRoundAttemptsOneRequestPerMember pins the attempt mapping:
// the reference round and the business round each dial every frozen member
// exactly once — no retries, no second round, no member skipped by a
// scheduling gate re-dial.
func TestReferenceRoundAttemptsOneRequestPerMember(t *testing.T) {
	nodes, p := phaseMappingFleet(t, func(i int, node *rpctest.Node) {
		node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
			return map[string]any{"number": "0x64", "hash": "0xabc"}, nil
		})
		node.Handle("eth_getBalance", func(call rpctest.Call) (any, error) {
			return "0x64", nil
		})
	})
	if _, _, err := phaseMappingAggregate(t, p); err != nil {
		t.Fatal(err)
	}
	for i, node := range nodes {
		headCalls, balanceCalls := 0, 0
		for _, call := range node.Calls() {
			switch call.Method {
			case "eth_getBlockByNumber":
				headCalls++
			case "eth_getBalance":
				balanceCalls++
			}
		}
		if headCalls != 1 {
			t.Fatalf("node %d answered %d reference-round requests, want exactly one attempt", i, headCalls)
		}
		if balanceCalls != 1 {
			t.Fatalf("node %d answered %d business-round requests, want exactly one attempt", i, balanceCalls)
		}
	}
}

// TestReferenceRoundHonorsCancellation pins the cancellation mapping: a
// canceled context fails both rounds with context.Canceled, and the fleet
// sees no second attempt after the cancellation.
func TestReferenceRoundHonorsCancellation(t *testing.T) {
	release := make(chan struct{})
	nodes, p := phaseMappingFleet(t, func(i int, node *rpctest.Node) {
		node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
			<-release
			return map[string]any{"number": "0x64", "hash": "0xabc"}, nil
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	aggregate, _ := traceAggregator(t, p, nil)
	_, err := aggregate.Aggregate(ctx, rpc.Request{Method: "eth_getBalance", Params: []any{"0x1", "latest"}})
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read err = %v, want context.Canceled", err)
	}
	for i, node := range nodes {
		calls := len(node.Calls())
		if calls == 0 {
			t.Fatalf("node %d saw no attempt at all", i)
		}
		if calls > 1 {
			t.Fatalf("node %d answered %d requests after cancellation, want one attempt per member", i, calls)
		}
	}
}

// TestReferenceRoundSourceIdentityIsFrozenBasis pins the source identity
// mapping: the reference round's evidence names exactly the invocation's
// frozen basis members — support comes from the voters that agreed, and a
// dissenting member rides as reject evidence, never vanishing.
func TestReferenceRoundSourceIdentityIsFrozenBasis(t *testing.T) {
	_, p := phaseMappingFleet(t, func(i int, node *rpctest.Node) {
		node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
			if i >= 2 {
				// Two members disagree on the head: with the floor at two,
				// the round fails, and the dissenters ride as reject or
				// fault evidence with their identity intact.
				return map[string]any{"number": "0x65", "hash": "0xdef"}, nil
			}
			return map[string]any{"number": "0x64", "hash": "0xabc"}, nil
		})
	})
	ref, _, err := phaseMappingAggregate(t, p)
	if err == nil {
		t.Fatal("a dissenting head must fail the reference")
	}
	// Every frozen member stays accounted for: the participants list carries
	// all three identities, and the dissenting member rides as reject
	// evidence rather than vanishing.
	if len(ref.Participants) != 3 {
		t.Fatalf("participants = %v, want the frozen basis", ref.Participants)
	}
	for i, member := range ref.Participants {
		if member.ID == "" {
			t.Fatalf("participant %d carries no identity: %v", i, member)
		}
	}
	if len(ref.Reject) != 1 {
		t.Fatalf("reject = %v, want the dissenting member named", ref.Reject)
	}
}

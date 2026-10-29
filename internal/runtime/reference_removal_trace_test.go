package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// threeHeadNodes builds a three-node fleet serving one common head.
func threeHeadNodes(t *testing.T) []*rpctest.Node {
	t.Helper()
	const (
		headHeight = 100
		headHash   = "0xabc"
	)
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	t.Cleanup(func() {
		for _, node := range nodes {
			node.Close()
		}
	})
	for _, node := range nodes {
		node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
			return map[string]any{"number": fmt.Sprintf("0x%x", headHeight), "hash": headHash}, nil
		})
	}
	return nodes
}

func removalFleetPool(t *testing.T, p *pool.Pool, nodes []*rpctest.Node) []*pool.Source {
	t.Helper()
	sources := make([]*pool.Source, 0, len(nodes))
	for i, node := range nodes {
		source, err := p.Join(fmt.Sprintf("node-%d", i), node.URL())
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source)
	}
	return sources
}

func removalStage(t *testing.T, p *pool.Pool) *ReferenceStage {
	t.Helper()
	return NewReferenceStage(ReferenceStageConfig{
		Pool:     p,
		Timeout:  2 * time.Second,
		MaxLag:   32,
		Recorder: observe.Nop,
	})
}

func frozenBasis(t *testing.T, sources []*pool.Source) rpc.Invocation {
	t.Helper()
	refs := make([]rpc.SourceRef, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, rpc.SourceRef{Name: source.Name(), URL: source.URL(), ID: source.ID()})
	}
	return rpc.NewInvocation(rpc.NewInvocationID(),
		rpc.FreezeBasis(refs, rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}))
}

// A member removed after an invocation froze its voting basis stays in the
// basis: the reference round still addresses the captured identity, and the
// member fails closed — no request, an ErrClosed-class fault — while the live
// members answer. The removal must never surface as an unknown-member
// rejection before any request: that would shrink the denominator instead of
// failing it closed, contradicting the pool's removal contract.
func TestRemovalBetweenRoundsKeepsBasisFailClosed(t *testing.T) {
	nodes := threeHeadNodes(t)
	p := pool.NewPoolWithOptions(0, pool.WithDialer(nativehttp.NewDialer()))
	t.Cleanup(p.Close)
	sources := removalFleetPool(t, p, nodes)
	stage := removalStage(t, p)

	invocation := frozenBasis(t, sources)
	request := rpc.ReferenceRequest{Kind: rpc.ReferenceLatest}

	// Before the removal the strict round corroborates over the whole basis.
	first, err := stage.Establish(context.Background(), invocation, request)
	if err != nil {
		t.Fatalf("establish before removal: %v", err)
	}
	ref, ok := first.Reference()
	if !ok || ref.Status != rpc.Corroborated || len(ref.Participants) != 3 {
		t.Fatalf("reference before removal = %+v, want corroborated over three participants", ref)
	}

	// An invocation that froze its basis before the caller removes one member
	// (an in-flight call paused between rounds, or a subsequent establishment
	// for another tag) re-runs the round on that same frozen basis. The
	// removed member keeps its basis slot and contributes a fail-closed fault;
	// the round never reports an unknown-member rejection before any request.
	if err := p.Remove("node-2"); err != nil {
		t.Fatalf("Remove node-2: %v", err)
	}
	afterRemoval := frozenBasis(t, sources) // same member set, frozen before the removal
	second, err := stage.Establish(context.Background(), afterRemoval, request)
	if err != nil {
		t.Fatalf("establish after removal = %v, want a fail-closed round, not an unknown-member rejection", err)
	}
	ref2, ok := second.Reference()
	if !ok || ref2.Status != rpc.Corroborated {
		t.Fatalf("reference after removal = %+v, want a corroborated round over the surviving members", ref2)
	}
	if len(ref2.Participants) != 3 {
		t.Fatalf("participants = %d, want the frozen basis kept whole (3)", len(ref2.Participants))
	}
	if len(ref2.Support) != 2 {
		t.Fatalf("support = %d, want only the two live members", len(ref2.Support))
	}
	if len(ref2.Reject) != 0 {
		t.Fatalf("reject = %v, want the removed member classified as a fault, not a rejection", ref2.Reject)
	}
	if len(ref2.SourceFaults) != 1 || ref2.SourceFaults[0].Source.ID != sources[2].ID() {
		t.Fatalf("source faults = %+v, want the removed member's slot", ref2.SourceFaults)
	}
	if !errors.Is(ref2.SourceFaults[0].Err, pool.ErrClosed) {
		t.Fatalf("removed member fault = %v, want ErrClosed", ref2.SourceFaults[0].Err)
	}
	// The unattempted removal keeps the human-readable fault list unchanged
	// (the pipeline's own contract); the structured attribution is where the
	// fail-closed member stays visible to reports.
	joined := false
	for _, fault := range ref2.Faults {
		if strings.Contains(fault.Error(), sources[2].Name()) {
			joined = true
		}
	}
	if joined {
		t.Fatal("the unattempted removal must not penalize the source's health list")
	}
}

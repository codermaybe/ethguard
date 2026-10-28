package aggregator

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// When membership changes between the reference round and the business round,
// the invocation keeps its frozen basis: the new member is not folded into
// the denominator, and the carrier still hands later phases the same frozen
// invocation.
func TestMembershipChangeBetweenRoundsKeepsFrozenBasis(t *testing.T) {
	p := pool.NewPool()
	sources := namedSources(t, "a", "b", "c")
	refs := make([]rpc.SourceRef, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, rpc.SourceRef{Name: source.Name(), ID: source.ID()})
	}
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	state := &invocationState{
		snap:    snap,
		snapOK:  true,
		members: sources,
	}
	state.ctx, state.invocation = beginInvocation(context.Background(), snap, true, sources, nil)

	// A member joins after the basis was frozen (the reference round ran).
	if _, err := p.Join("late", "http://127.0.0.1:9199"); err != nil {
		t.Fatal(err)
	}
	basis := state.invocation.Basis()
	if basis.Size() != 3 {
		t.Fatalf("basis size = %d, want the three captured members", basis.Size())
	}
	if basis.Required() != 2 {
		t.Fatalf("basis required = %d, want the frozen threshold", basis.Required())
	}
	seen := map[string]bool{}
	for _, ref := range basis.Members {
		seen[ref.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("basis members = %v, want exactly the captured three", seen)
	}
	carried, ok := refcontext.Invocation(state.ctx)
	if !ok || carried.ID() != state.invocation.ID() {
		t.Fatal("the invocation carrier lost the frozen value")
	}
}

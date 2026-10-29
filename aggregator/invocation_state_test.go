package aggregator

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/rpc"
)

// The invocation's basis is frozen at entry: later health or scheduling state
// cannot fold a new member into the denominator, and the carrier still hands
// later phases the same frozen invocation.
func TestInvocationBasisStaysFrozenAcrossRounds(t *testing.T) {
	sources := namedSources(t, "a", "b", "c")
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	state := &invocationState{
		snap:    snap,
		snapOK:  true,
		members: sources,
	}
	state.ctx, state.invocation = beginInvocation(context.Background(), snap, true, sources, nil)

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

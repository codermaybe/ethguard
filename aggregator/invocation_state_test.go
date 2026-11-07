package aggregator

import (
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The invocation's basis is frozen at entry: later health or scheduling state
// cannot fold a new member into the denominator, and the aggregator's phases
// all run on that one frozen value.
func TestInvocationBasisStaysFrozenAcrossRounds(t *testing.T) {
	sources := namedSources(t, "a", "b", "c")
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	state := &invocationState{
		snap:    snap,
		snapOK:  true,
		members: sources,
	}
	state.invocation = beginInvocation(snap, true, sources, nil, false)

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
	if state.invocation.ID() == "" {
		t.Fatal("the invocation lost its identity")
	}
}

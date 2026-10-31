package aggregator

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// The planned invocation's capability consumption. The pipeline reads the
// resolver class from the plan and the resolver value's capabilities from
// the value itself — never by walking decorators — so the corroboration
// snapshot reaches exactly the classes that declare it, and the per-source
// verdicts come from the resolver's own statement.

// planFor compiles one registered method's plan.
func planFor(t *testing.T, method string) rpc.ReadPlan {
	t.Helper()
	for _, spec := range methods.Specs() {
		if spec.Method == method {
			plan, err := methods.CompilePlan(spec)
			if err != nil {
				t.Fatalf("%s: compile plan: %v", method, err)
			}
			return plan
		}
	}
	t.Fatalf("%s is not a registered read method", method)
	return rpc.ReadPlan{}
}

func quantityObservations() []rpc.Observation {
	return []rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(`"0x64"`)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x64"`)},
	}
}

// TestPlannedCallRevertStatesItsExecutionVerdicts pins the seam the planned
// eth_call resolves through: under the invocation's frozen snapshot the
// resolver's corroborated outcome carries the decision and the per-source
// execution verdicts, so classification never falls back to re-running the
// resolver's comparison key as a usability guess.
func TestPlannedCallRevertStatesItsExecutionVerdicts(t *testing.T) {
	plan := planFor(t, methods.MethodCall)
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	res := resolvePlannedInvocation(&plan, rpc.Request{Method: plan.Method}, snap, true, quantityObservations())
	if res.err != nil {
		t.Fatalf("resolve: %v", res.err)
	}
	if res.decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v, want a corroborated successful call", res.decision.Status)
	}
	if len(res.verdicts) != 2 {
		t.Fatalf("verdicts = %+v, want one statement per source from the resolver itself", res.verdicts)
	}
	for _, verdict := range res.verdicts {
		if verdict.Kind != rpc.VerdictSupported {
			t.Fatalf("verdict for %s = %v", verdict.Source.ID, verdict.Kind)
		}
	}
}

// TestPlannedSamplingClassesNeverCorroborate pins the other side of the
// capability contract at the invocation seam: even with a corroboration
// snapshot present, a median or local read resolves through its own plain
// entry and stays Selected at best — the snapshot never raises a variable or
// node-local answer into a corroboration claim, and no verdicts are invented
// for sources that were never classified.
func TestPlannedSamplingClassesNeverCorroborate(t *testing.T) {
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	for _, method := range []string{methods.MethodGasPrice, methods.MethodPeerCount} {
		plan := planFor(t, method)
		res := resolvePlannedInvocation(&plan, rpc.Request{Method: plan.Method}, snap, true, quantityObservations())
		if res.err != nil {
			t.Fatalf("%s: resolve: %v", method, res.err)
		}
		if res.decision.Status != rpc.Selected {
			t.Fatalf("%s: status = %v, want Selected", method, res.decision.Status)
		}
		if res.verdicts != nil {
			t.Fatalf("%s: verdicts = %+v, want none: a sampling class does not state per-source verdicts", method, res.verdicts)
		}
	}
}

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
		{Attempted: true, Source: rpc.SourceRef{Name: "a", ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x64"`)}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x64"`)}}},
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
	outcome, err := resolvePlannedInvocation(&plan, snap, true, false, quantityObservations())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if outcome.Decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v, want a corroborated successful call", outcome.Decision.Status)
	}
	if len(outcome.Verdicts) != 2 {
		t.Fatalf("verdicts = %+v, want one statement per source from the resolver itself", outcome.Verdicts)
	}
	for _, verdict := range outcome.Verdicts {
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
		outcome, err := resolvePlannedInvocation(&plan, snap, true, false, quantityObservations())
		if err != nil {
			t.Fatalf("%s: resolve: %v", method, err)
		}
		if outcome.Decision.Status != rpc.Selected {
			t.Fatalf("%s: status = %v, want Selected", method, outcome.Decision.Status)
		}
		if err := outcome.Validate(quantityObservations()); err != nil {
			t.Fatal(err)
		}
		if method == methods.MethodPeerCount && outcome.Verdicts[1].Kind != rpc.VerdictUnassessed {
			t.Fatal("local tail was interpreted")
		}
	}
}

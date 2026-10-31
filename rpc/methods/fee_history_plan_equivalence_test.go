package methods

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The eth_feeHistory seam boundary. The plan path is the current source of
// facts: its plain strict resolver owns the full outcome capability by its
// own methods. The legacy FeeHistoryPolicy wrapper declares a corroborated
// outcome variant only; its assembled chain reaches the plain outcome shape
// through the decorator base's conditional forwarding, which keeps the two
// assemblies observationally identical while leaving the wrapper's declared
// capability set at the compatibility surface. The default runtime never
// assembles the wrapper, and nothing in the plan seam depends on the
// wrapper's capability shape — the tests below pin the behavioral
// equivalence that the compatibility contract holds, not a new rule.

func feeHistoryPlan(t *testing.T) rpc.ReadPlan {
	t.Helper()
	for _, spec := range Specs() {
		if spec.Method == MethodFeeHistory {
			plan, err := CompilePlan(spec)
			if err != nil {
				t.Fatalf("compile feeHistory plan: %v", err)
			}
			return plan
		}
	}
	t.Fatal("eth_feeHistory is not a registered read method")
	return rpc.ReadPlan{}
}

func feeHistoryObservations() []rpc.Observation {
	payload := feeHistory("0x1", "0.5", "0x3")
	return []rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(payload)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(payload)},
	}
}

// TestFeeHistoryPlanPathOwnsTheFullOutcome pins the facts on the plan side:
// the compiled plan's plain strict resolver states per-source verdicts
// through both outcome entry points, and two agreeing fee-history responses
// corroborate under the invocation snapshot.
func TestFeeHistoryPlanPathOwnsTheFullOutcome(t *testing.T) {
	plan := feeHistoryPlan(t)
	outcome, ok := plan.Resolver.(rpc.OutcomeResolver)
	if !ok {
		t.Fatal("the plan's feeHistory resolver does not state plain outcomes")
	}
	stated, err := outcome.ResolveOutcome(feeHistoryObservations())
	if err != nil {
		t.Fatalf("plain outcome: %v", err)
	}
	if stated.Decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v, want the legacy agreement shape's Corroborated for two agreeing sources", stated.Decision.Status)
	}
	corroborating, ok := plan.Resolver.(rpc.CorroboratingOutcomeResolver)
	if !ok {
		t.Fatal("the plan's feeHistory resolver does not state corroborated outcomes")
	}
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	outcomeCorroborated, err := corroborating.ResolveOutcomeCorroborated(snap, feeHistoryObservations())
	if err != nil {
		t.Fatalf("corroborated outcome: %v", err)
	}
	if outcomeCorroborated.Decision.Status != rpc.Corroborated || len(outcomeCorroborated.Verdicts) != 2 {
		t.Fatalf("corroborated outcome = %+v with verdicts %+v", outcomeCorroborated.Decision, outcomeCorroborated.Verdicts)
	}
}

// TestFeeHistoryPlanAndWrapperBehaveAlike holds the two assemblies together
// on their public behavior: the same observations resolve to the same
// decision through plain resolution, plain outcomes, and corroborated
// outcomes. The comparison is behavioral on purpose — the wrapper's
// capability shape is a compatibility fact judged by what it answers, not
// by which method layer declared it.
func TestFeeHistoryPlanAndWrapperBehaveAlike(t *testing.T) {
	plan := feeHistoryPlan(t)
	wrapper := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 16, nil })
	obs := feeHistoryObservations()
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}

	planPlain, planErr := plan.Resolver.Resolve(obs)
	wrapperPlain, wrapperErr := wrapper.Resolve(obs)
	if (planErr == nil) != (wrapperErr == nil) || planErr != nil && planErr.Error() != wrapperErr.Error() {
		t.Fatalf("resolve mismatch: %v vs %v", planErr, wrapperErr)
	}
	if !reflect.DeepEqual(faultless(planPlain), faultless(wrapperPlain)) {
		t.Fatalf("decisions diverged:\nplan     %+v\nwrapper  %+v", planPlain, wrapperPlain)
	}

	planOutcome, planOutcomeErr := plan.Resolver.(rpc.OutcomeResolver).ResolveOutcome(obs)
	if planOutcomeErr != nil {
		t.Fatalf("plan plain outcome: %v", planOutcomeErr)
	}
	wrapperOutcome, wrapperOutcomeErr := wrapper.(rpc.OutcomeResolver).ResolveOutcome(obs)
	if wrapperOutcomeErr != nil {
		t.Fatalf("wrapper plain outcome: %v", wrapperOutcomeErr)
	}
	if !reflect.DeepEqual(planOutcome.Verdicts, wrapperOutcome.Verdicts) {
		t.Fatalf("plain outcome verdicts diverged:\nplan     %+v\nwrapper  %+v", planOutcome.Verdicts, wrapperOutcome.Verdicts)
	}
	if !reflect.DeepEqual(faultless(planOutcome.Decision), faultless(wrapperOutcome.Decision)) {
		t.Fatalf("plain outcome decisions diverged:\nplan     %+v\nwrapper  %+v", planOutcome.Decision, wrapperOutcome.Decision)
	}

	planCorroborated, err := plan.Resolver.(rpc.CorroboratingOutcomeResolver).ResolveOutcomeCorroborated(snap, obs)
	if err != nil {
		t.Fatalf("plan corroborated outcome: %v", err)
	}
	wrapperCorroborated, err := wrapper.(rpc.CorroboratingOutcomeResolver).ResolveOutcomeCorroborated(snap, obs)
	if err != nil {
		t.Fatalf("wrapper corroborated outcome: %v", err)
	}
	if !reflect.DeepEqual(faultless(planCorroborated.Decision), faultless(wrapperCorroborated.Decision)) {
		t.Fatalf("corroborated decisions diverged:\nplan     %+v\nwrapper  %+v", planCorroborated.Decision, wrapperCorroborated.Decision)
	}
	if !reflect.DeepEqual(planCorroborated.Verdicts, wrapperCorroborated.Verdicts) {
		t.Fatalf("corroborated verdicts diverged:\nplan     %+v\nwrapper  %+v", planCorroborated.Verdicts, wrapperCorroborated.Verdicts)
	}
}

// faultless keeps the behavioral comparison free of the fault-slice's
// allocation shape; status, value, provenance, support, and dimensions stay
// in the comparison.
func faultless(decision rpc.Decision) rpc.Decision {
	decision.Faults = nil
	return decision
}

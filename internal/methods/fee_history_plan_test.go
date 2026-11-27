package methods

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// The eth_feeHistory plan seam. The compiled plan is the only assembly: its
// plain strict resolver owns the full outcome capability, and the tests below
// verify the plan's behavior directly.
func feeHistoryPlan(t *testing.T) rpcengine.ReadPlan {
	t.Helper()
	return compiledPlan(t, MethodFeeHistory)
}

func feeHistoryObservations() []rpc.Observation {
	payload := feeHistory("0x1", "0.5", "0x3")
	return []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{Name: "a", ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(payload)}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(payload)}}},
	}
}

// TestFeeHistoryPlanPathOwnsTheFullOutcome pins the facts on the plan side:
// the compiled plan's plain strict resolver states per-source verdicts
// through both outcome entry points, and two agreeing fee-history responses
// corroborate under the invocation snapshot.
func TestFeeHistoryPlanPathOwnsTheFullOutcome(t *testing.T) {
	plan := feeHistoryPlan(t)
	outcome := plan.Resolver
	ok := outcome != nil
	if !ok {
		t.Fatal("the plan's feeHistory resolver does not state plain outcomes")
	}
	stated, err := outcome.Resolve(feeHistoryObservations())
	if err != nil {
		t.Fatalf("plain outcome: %v", err)
	}
	if stated.Decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v, want the legacy agreement shape's Corroborated for two agreeing sources", stated.Decision.Status)
	}
	corroborating, ok := plan.Resolver.(rpcengine.CorroboratingResolver)
	if !ok {
		t.Fatal("the plan's feeHistory resolver does not state corroborated outcomes")
	}
	snap := rpcengine.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	outcomeCorroborated, err := corroborating.ResolveCorroborated(snap, feeHistoryObservations())
	if err != nil {
		t.Fatalf("corroborated outcome: %v", err)
	}
	if outcomeCorroborated.Decision.Status != rpc.Corroborated || len(outcomeCorroborated.Verdicts) != 2 {
		t.Fatalf("corroborated outcome = %+v with verdicts %+v", outcomeCorroborated.Decision, outcomeCorroborated.Verdicts)
	}
}

// TestFeeHistoryPlanResolvesAgreement pins the plan's public behavior
// directly: two agreeing responses corroborate under the invocation
// snapshot with both sources in support, and the plain resolution reaches
// the same decision.
func TestFeeHistoryPlanResolvesAgreement(t *testing.T) {
	plan := feeHistoryPlan(t)
	obs := feeHistoryObservations()
	snap := rpcengine.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}

	plain, plainErr := plan.Resolver.Resolve(obs)
	if plainErr != nil {
		t.Fatalf("plain resolve: %v", plainErr)
	}
	if plain.Decision.Status != rpc.Corroborated || len(plain.Decision.Support) != 2 {
		t.Fatalf("plain resolve = %+v, want corroborated agreement", plain)
	}

	planCorroborated, err := plan.Resolver.(rpcengine.CorroboratingResolver).ResolveCorroborated(snap, obs)
	if err != nil {
		t.Fatalf("corroborated outcome: %v", err)
	}
	if !reflect.DeepEqual(faultless(planCorroborated.Decision), faultless(plain.Decision)) {
		t.Fatalf("corroborated outcome re-decided the value:\noutcome %+v\nplain   %+v", planCorroborated.Decision, plain)
	}
	if len(planCorroborated.Verdicts) != 2 {
		t.Fatalf("verdicts = %+v, want one statement per source", planCorroborated.Verdicts)
	}
}

// faultless keeps the behavioral comparison free of the fault-slice's
// allocation shape; status, value, provenance, support, and dimensions stay
// in the comparison.
func faultless(decision rpc.Decision) rpc.Decision {
	decision.Faults = nil
	// Plain strict resolution has no configured support floor.
	decision.Adjudication.Required = 0
	return decision
}

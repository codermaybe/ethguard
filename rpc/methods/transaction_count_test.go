package methods

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestTransactionCountPlanKeepsNullForNotFoundProjection(t *testing.T) {
	plan, err := PlanFor(MethodGetBlockTransactionCountByHash)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Corroborating() || plan.Pending.Mode != PendingNone {
		t.Fatalf("plan = %+v, want strict immutable read without pending", plan)
	}
	if got, err := plan.Resolver.Normalize(json.RawMessage(`null`)); err != nil || got != "null" {
		t.Fatalf("null normalization = %q, %v; want null key", got, err)
	}
	if got, err := plan.Resolver.Normalize(json.RawMessage(`"0x2"`)); err != nil || got != "2" {
		t.Fatalf("quantity normalization = %q, %v; want decimal 2", got, err)
	}
	resolver, ok := plan.Resolver.(rpc.CorroboratingPolicy)
	if !ok {
		t.Fatal("transaction count resolver is not corroborating")
	}
	decision, err := resolver.ResolveCorroborated(rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: json.RawMessage(`null`)},
		{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`null`)},
		{Source: rpc.SourceRef{ID: "c"}, Value: json.RawMessage(`null`)},
	})
	if err != nil || string(decision.Value) != "null" || decision.Status != rpc.Corroborated {
		t.Fatalf("null decision = %+v, %v", decision, err)
	}
}

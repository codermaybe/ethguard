package methods

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// TestEstimateGasObjectForms pins the estimate-gas plan: the EIP-1898 object
// form rewrites to the established hash, invalid objects fail before any
// establishment, pending stays a node-local observation (Selected at best,
// first source), and the mined path is the fleet median.
func TestEstimateGasObjectForms(t *testing.T) {
	plan := compiledPlan(t, MethodEstimateGas)
	call := map[string]any{"to": "0xabc", "data": "0x18160ddd"}

	t.Run("success rewrites moving-tag object and stays selected", func(t *testing.T) {
		obj := map[string]any{"blockNumber": "latest", "requireCanonical": true}
		prepared, pin, err := preparePlanRequest(t, plan, rpc.Request{
			Method: MethodEstimateGas, Params: []any{call, obj},
		})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := prepared.Params[1].(map[string]any)
		if !ok || got["blockHash"] != fixtureHash || got["requireCanonical"] != true {
			t.Fatalf("rewrote to %#v", prepared.Params[1])
		}
		if pin != fixtureHash {
			t.Fatalf("pin = %q, want %s", pin, fixtureHash)
		}
		sources := []rpc.SourceRef{{ID: "a"}, {ID: "b"}, {ID: "c"}}
		decision, err := plan.Resolver.Resolve([]rpc.Observation{
			{Source: sources[0], Value: json.RawMessage(`"0x64"`)},
			{Source: sources[1], Value: json.RawMessage(`"0x66"`)},
			{Source: sources[2], Value: json.RawMessage(`"0x68"`)},
		})
		if err != nil || decision.Status != rpc.Selected || string(decision.Value) != `"0x66"` {
			t.Fatalf("median = %+v %v", decision, err)
		}
		if decision.Status == rpc.Corroborated {
			t.Fatal("estimate claimed Corroborated")
		}
	})

	t.Run("disagreement stays a selected median", func(t *testing.T) {
		decision, err := plan.Resolver.Resolve([]rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Value: json.RawMessage(`"0x10"`)},
			{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`"0x30"`)},
		})
		if err != nil || decision.Status != rpc.Selected || len(decision.Reject) != 1 {
			t.Fatalf("disagreement = %+v %v", decision, err)
		}
	})

	t.Run("invalid object fails before establishment", func(t *testing.T) {
		for _, obj := range []map[string]any{
			{"blockHash": testHashExplicit, "blockNumber": "0x1"},
			{"blockNumber": "nope"},
			{"blockHash": "0x1234"},
			{"extra": true},
		} {
			if _, _, err := preparePlanRequest(t, plan, rpc.Request{
				Method: MethodEstimateGas, Params: []any{call, obj},
			}); !errors.Is(err, rpc.ErrInvalidRequest) {
				t.Fatalf("%v: got %v", obj, err)
			}
		}
	})

	t.Run("pending string and object pass through", func(t *testing.T) {
		for _, ref := range []any{"pending", map[string]any{"blockNumber": "PENDING"}} {
			prepared, _, err := preparePlanRequest(t, plan, rpc.Request{
				Method: MethodEstimateGas, Params: []any{call, ref},
			})
			if err != nil {
				t.Fatalf("%v: %v", ref, err)
			}
			switch want := ref.(type) {
			case string:
				if prepared.Params[1] != want {
					t.Fatalf("string pending rewritten: %#v", prepared.Params[1])
				}
			case map[string]any:
				got := prepared.Params[1].(map[string]any)
				if got["blockNumber"] != "PENDING" {
					t.Fatalf("object pending rewritten: %#v", got)
				}
			}
		}
		decision, err := plan.Resolver.Resolve([]rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Value: json.RawMessage(`"0x64"`)},
			{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`"0x64"`)},
		})
		if err != nil || decision.Status != rpc.Selected {
			t.Fatalf("pending agreement = %+v %v", decision, err)
		}
	})

	t.Run("pending is the first healthy source, not the fleet median", func(t *testing.T) {
		if plan.Pending.Mode != rpc.PendingObserve || plan.Pending.Pos != 1 {
			t.Fatalf("pending declaration = %+v, want observe at 1", plan.Pending)
		}
		decision, err := rpc.ResolveLocalObservation(plan.Resolver, []rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Value: json.RawMessage(`"0x10"`)},
			{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`"0x30"`)},
			{Source: rpc.SourceRef{ID: "c"}, Value: json.RawMessage(`"0x20"`)},
		})
		if err != nil || decision.Status != rpc.Selected || string(decision.Value) != `"0x10"` || len(decision.Support) != 1 || decision.Support[0].ID != "a" {
			t.Fatalf("pending local = %+v %v", decision, err)
		}
		if decision.Status == rpc.Corroborated {
			t.Fatal("pending estimate claimed Corroborated")
		}
	})

	t.Run("provider errors stay unknown", func(t *testing.T) {
		decision, err := plan.Resolver.Resolve([]rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Err: errors.New("transport")},
			{Source: rpc.SourceRef{ID: "b"}, Err: errors.New("rpc")},
		})
		if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown {
			t.Fatalf("provider error = %+v %v", decision, err)
		}
		if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("provider error reclassified: %v", err)
		}
	})
}

// TestCreateAccessListObjectForms pins the access-list plan: the EIP-1898
// object form rewrites and corroborates, conflicts fail closed, malformed
// objects fail locally, and pending stays a node-local observation.
func TestCreateAccessListObjectForms(t *testing.T) {
	plan := compiledPlan(t, MethodCreateAccessList)
	call := map[string]any{"to": "0xabc", "data": "0x18160ddd"}
	body := json.RawMessage(`{"accessList":[{"address":"0x0000000000000000000000000000000000000ABC","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001"]}],"gasUsed":"0x5208"}`)
	other := json.RawMessage(`{"accessList":[{"address":"0x0000000000000000000000000000000000000ABC","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000002"]}],"gasUsed":"0x5208"}`)

	t.Run("success rewrites moving-tag object and corroborates", func(t *testing.T) {
		prepared, pin, err := preparePlanRequest(t, plan, rpc.Request{
			Method: MethodCreateAccessList,
			Params: []any{call, map[string]any{"blockNumber": "safe", "requireCanonical": true}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := prepared.Params[1].(map[string]any)
		if got["blockHash"] != fixtureHash || got["requireCanonical"] != true {
			t.Fatalf("rewrote to %#v", got)
		}
		if pin != fixtureHash {
			t.Fatalf("pin = %q, want %s", pin, fixtureHash)
		}
		corroborating, ok := plan.Resolver.(rpc.CorroboratingPolicy)
		if !ok {
			t.Fatal("lost the corroborating resolver")
		}
		decision, err := corroborating.ResolveCorroborated(rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}, []rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Value: body},
			{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`{"gasUsed":"0x5208","accessList":[{"address":"0x0000000000000000000000000000000000000abc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001"]}]}`)},
		})
		if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
			t.Fatalf("success = %+v %v", decision, err)
		}
	})

	t.Run("conflicting payloads stay unknown", func(t *testing.T) {
		corroborating := plan.Resolver.(rpc.CorroboratingPolicy)
		decision, err := corroborating.ResolveCorroborated(rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}, []rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Value: body},
			{Source: rpc.SourceRef{ID: "b"}, Value: other},
		})
		if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
			t.Fatalf("conflict = %+v %v", decision, err)
		}
	})

	t.Run("invalid object fails before establishment", func(t *testing.T) {
		if _, _, err := preparePlanRequest(t, plan, rpc.Request{
			Method: MethodCreateAccessList,
			Params: []any{call, map[string]any{"blockHash": testHashExplicit, "blockNumber": "latest"}},
		}); !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("invalid = %v", err)
		}
	})

	t.Run("pending object is local and does not anchor", func(t *testing.T) {
		if plan.Pending.Mode != rpc.PendingObserve || plan.Pending.Pos != 1 {
			t.Fatalf("pending declaration = %+v, want observe at 1", plan.Pending)
		}
		ref := map[string]any{"blockNumber": "pending", "requireCanonical": true}
		req := rpc.Request{Method: MethodCreateAccessList, Params: []any{call, ref}}
		evidence, needed := establishedEvidence(t, plan, req)
		if needed {
			t.Fatal("pending declared a reference need")
		}
		prepared, _, err := plan.PrepareWithReference(req, evidence, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := prepared.Params[1].(map[string]any); got["blockNumber"] != "pending" {
			t.Fatalf("pending object rewritten: %#v", got)
		}
		decision, err := rpc.ResolveLocalObservation(plan.Resolver, []rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Value: body},
			{Source: rpc.SourceRef{ID: "b"}, Value: body},
		})
		if err != nil || decision.Status != rpc.Selected || len(decision.Support) != 1 {
			t.Fatalf("pending = %+v %v", decision, err)
		}
	})

	t.Run("provider errors stay unknown", func(t *testing.T) {
		decision, err := plan.Resolver.Resolve([]rpc.Observation{
			{Source: rpc.SourceRef{ID: "a"}, Err: errors.New("down")},
			{Source: rpc.SourceRef{ID: "b"}, Err: errors.New("down")},
		})
		if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown {
			t.Fatalf("provider error = %+v %v", decision, err)
		}
	})
}

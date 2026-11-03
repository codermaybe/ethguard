package methods

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func pendingObs(values ...string) []rpc.Observation {
	obs := make([]rpc.Observation, 0, len(values))
	for i, v := range values {
		obs = append(obs, rpc.Observation{
			Source: rpc.SourceRef{Name: string(rune('a' + i)), ID: string(rune('a' + i))},
			Value:  json.RawMessage(v),
		})
	}
	return obs
}

// resolveLocalThroughPlan resolves pending observations the way the
// aggregator's pending-local route does: the shared local-observation
// resolution over the plan's own resolver.
func resolveLocalThroughPlan(t *testing.T, plan rpc.ReadPlan, obs []rpc.Observation) rpc.Decision {
	t.Helper()
	decision, err := rpc.ResolveLocalObservation(plan.Resolver, obs)
	if err != nil {
		t.Fatalf("resolve local: %v", err)
	}
	return decision
}

// TestPendingLocalSlice pins decision 0035 for the five default-block state
// reads: pending keeps its node-state meaning — the declaration routes the
// pending shape to a local observation, Selected at best, never Corroborated
// — while every other shape keeps strict corroboration.
func TestPendingLocalSlice(t *testing.T) {
	t.Run("pending is a declared local observation at the anchored position", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			if tc.plan.Pending.Mode != rpc.PendingObserve {
				t.Fatalf("%s pending mode = %q, want local observation", tc.name, tc.plan.Pending.Mode)
			}
			if tc.plan.Pending.Pos != tc.pos {
				t.Fatalf("%s pending position = %d, want %d", tc.name, tc.plan.Pending.Pos, tc.pos)
			}
			// The declaration never installs a local pending rejection: the
			// pending shape must reach the providers untouched.
			if tc.plan.PendingReject != nil {
				if err := tc.plan.PendingReject(rpc.Request{Method: tc.name, Params: withRef([]any{"0xabc"}, tc.pos, "pending")}); err != nil {
					t.Fatalf("%s rejected the pending spelling locally: %v", tc.name, err)
				}
			}
		}
	})

	t.Run("unanimous pending agreement stays Selected", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			var value string
			switch tc.name {
			case "eth_getStorageAt":
				value = `"0x` + repeat00(32) + `"`
			case "eth_getCode", "eth_call":
				value = `"0x1234"`
			default:
				value = `"0x64"`
			}
			decision := resolveLocalThroughPlan(t, tc.plan, pendingObs(value, value, value))
			if decision.Status != rpc.Selected {
				t.Fatalf("%s pending unanimous: status = %v, want selected (never corroborated)", tc.name, decision.Status)
			}
			if len(decision.Support) != 1 {
				t.Fatalf("%s pending: support = %v, want exactly the first source", tc.name, decision.Support)
			}
		}
	})

	t.Run("pending dissent is not conflict", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			var first, second, third string
			switch tc.name {
			case "eth_getStorageAt":
				first, second, third = `"0x`+repeat00(32)+`"`, `"0x11`+repeat00(31)+`"`, `"0x22`+repeat00(31)+`"`
			case "eth_getCode", "eth_call":
				first, second, third = `"0x64"`, `"0x65"`, `"0x66"`
			default:
				first, second, third = `"0x64"`, `"0x65"`, `"0x66"`
			}
			obs := pendingObs(first, second, third)
			decision, err := rpc.ResolveLocalObservation(tc.plan.Resolver, obs)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if decision.Status != rpc.Selected || string(decision.Value) != first {
				t.Fatalf("%s pending split: status = %v value = %s, want selected first answer", tc.name, decision.Status, decision.Value)
			}
		}
	})

	t.Run("pending without a usable source fails Unknown", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			failed := []rpc.Observation{
				{Source: rpc.SourceRef{Name: "a", ID: "a"}, Err: errors.New("down")},
				{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"not-hex"`)},
			}
			decision, err := rpc.ResolveLocalObservation(tc.plan.Resolver, failed)
			if err == nil || !errors.Is(err, rpc.ErrNoValidResponse) {
				t.Fatalf("%s: got %v, want ErrNoValidResponse", tc.name, err)
			}
			if decision.Status != rpc.Unknown {
				t.Fatalf("%s: failed status = %v, want unknown", tc.name, decision.Status)
			}
		}
	})

	t.Run("pending keeps winner evidence", func(t *testing.T) {
		balance := compiledPlan(t, MethodGetBalance)
		obs := pendingObs(`"0x64"`)
		obs[0].Evidence = json.RawMessage(`{"admitted":true}`)
		decision := resolveLocalThroughPlan(t, balance, obs)
		if string(decision.Provenance) != `{"admitted":true}` {
			t.Fatalf("evidence = %s, want winner evidence", decision.Provenance)
		}
	})

	t.Run("pending eth_call keeps revert observations", func(t *testing.T) {
		call := compiledPlan(t, MethodCall)
		revert := &rpc.RPCError{Code: -32000, Message: "execution reverted"}
		obs := []rpc.Observation{{Source: rpc.SourceRef{Name: "a", ID: "a"}, Err: errors.New("execution reverted"), RPCError: revert}}
		decision, err := rpc.ResolveLocalObservation(call.Resolver, obs)
		if err != nil {
			t.Fatalf("pending revert: %v", err)
		}
		if decision.Status != rpc.Selected {
			t.Fatalf("pending revert status = %v, want selected", decision.Status)
		}
		var envelope struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(decision.Value, &envelope); err != nil || envelope.Code != -32000 {
			t.Fatalf("pending revert value = %s, want the revert envelope", decision.Value)
		}
	})

	t.Run("mined shapes keep strict corroboration", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			var value string
			switch tc.name {
			case "eth_getStorageAt":
				value = `"0x` + repeat00(32) + `"`
			case "eth_getCode", "eth_call":
				value = `"0x1234"`
			default:
				value = `"0x64"`
			}
			corroborating, ok := tc.plan.Resolver.(rpc.CorroboratingPolicy)
			if !ok {
				t.Fatalf("%s lost the corroborating resolver", tc.name)
			}
			decision, err := corroborating.ResolveCorroborated(rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}, pendingObs(value, value))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if decision.Status != rpc.Corroborated {
				t.Fatalf("%s unanimous mined: status = %v, want corroborated", tc.name, decision.Status)
			}
		}
	})

	t.Run("capabilities follow the plan's declarations", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			if !tc.plan.Corroborating() {
				t.Fatalf("%s lost the corroborating class", tc.name)
			}
			// The pending capability is the plan's declaration: the
			// aggregator routes the pending shape to the local-observation
			// resolution off it, not off a wrapper type.
			if tc.plan.Pending.Mode != rpc.PendingObserve {
				t.Fatalf("%s lost the pending-observation declaration", tc.name)
			}
			if agreement, ok := tc.plan.Resolver.(rpc.AgreementPolicy); !ok || !agreement.RequiresAgreement() {
				t.Fatalf("%s lost RequiresAgreement (mined-path adapter gate)", tc.name)
			}
		}
	})
}

func repeat00(n int) string {
	out := ""
	for range n {
		out += "00"
	}
	return out
}

// TestPendingLocalExtendedMethods pins decision 0035 beyond the first
// slice: access-list creation, fee history, and block-by-number serve
// pending as a node-local observation too. eth_estimateGas does as well,
// but its mined path is Median, not a corroborating policy, so its pending
// lock lives with TestEstimateGasObjectForms. Gas-price Median methods have
// no pending form and stay unwrapped. Logs carries its range in filter
// fields and stays a documented T03 item.
func TestPendingLocalExtendedMethods(t *testing.T) {
	cases := []struct {
		name   string
		plan   rpc.ReadPlan
		params []any
		pos    int
	}{
		{"eth_createAccessList", compiledPlan(t, MethodCreateAccessList), []any{map[string]any{"to": "0xabc"}, "pending"}, 1},
		{"eth_feeHistory", compiledPlan(t, MethodFeeHistory), []any{uint64(4), "pending", []float64{}}, 1},
		{"eth_getBlockByNumber", compiledPlan(t, MethodGetBlockByNumber), []any{"pending", false}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plan.Pending.Mode != rpc.PendingObserve || tc.plan.Pending.Pos != tc.pos {
				t.Fatalf("%s pending declaration = %+v, want observe at %d", tc.name, tc.plan.Pending, tc.pos)
			}
			// Pending rides the wire untouched: no establishment is needed to
			// prepare a pending request, whatever the reference evidence holds.
			req := rpc.Request{Method: tc.name, Params: tc.params}
			evidence, needed := establishedEvidence(t, tc.plan, req)
			if needed {
				t.Fatalf("%s pending declared a reference need", tc.name)
			}
			prepared, _, err := tc.plan.PrepareWithReference(req, evidence, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(prepared.Params) != len(tc.params) {
				t.Fatalf("%s pending request reshaped: %#v", tc.name, prepared.Params)
			}
			if _, ok := tc.plan.Resolver.(rpc.CorroboratingPolicy); !ok {
				t.Fatalf("%s lost CorroboratingPolicy", tc.name)
			}
		})
	}
}

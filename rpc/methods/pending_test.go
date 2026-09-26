package methods

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"ethguard/rpc"
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

// TestPendingLocalSlice pins decision 0035 for the five default-block state
// reads: pending keeps its node-state meaning and resolves as a local
// observation — Selected at best, never Corroborated — while every other
// shape keeps strict corroboration.
func TestPendingLocalSlice(t *testing.T) {
	t.Run("pending spellings are local observations", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			lp, ok := policy.(rpc.LocalObservationPolicy)
			if !ok {
				t.Fatalf("%s does not expose LocalObservationPolicy", tc.name)
			}
			for _, ref := range []any{
				"pending", "Pending", "PENDING",
				map[string]any{"blockNumber": "pending"},
				map[string]any{"blockNumber": "pending", "requireCanonical": true},
			} {
				prepared, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, ref),
				})
				if err != nil {
					t.Fatalf("%s %v: %v", tc.name, ref, err)
				}
				if !lp.IsLocalObservation(prepared) {
					t.Fatalf("%s %v: not detected as pending-local", tc.name, ref)
				}
				if len(anchor.seen) != 0 {
					t.Fatalf("%s %v consulted anchor", tc.name, ref)
				}
			}
			for _, ref := range []any{
				"latest", "safe", "finalized", "earliest", "0x10", testHashExplicit,
				map[string]any{"blockHash": testHashExplicit},
				map[string]any{"blockNumber": "0x10"},
				map[string]any{"blockNumber": "latest"},
			} {
				prepared, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, ref),
				})
				if err != nil {
					t.Fatalf("%s %v: %v", tc.name, ref, err)
				}
				if lp.IsLocalObservation(prepared) {
					t.Fatalf("%s %v: misdetected as pending-local", tc.name, ref)
				}
			}
		}
	})

	t.Run("unanimous pending agreement stays Selected", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			lp := policy.(rpc.LocalObservationPolicy)
			var value string
			switch tc.name {
			case "eth_getStorageAt":
				value = `"0x` + repeat00(32) + `"`
			case "eth_getCode", "eth_call":
				value = `"0x1234"`
			default:
				value = `"0x64"`
			}
			decision, err := lp.ResolveLocal(pendingObs(value, value, value))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if decision.Status != rpc.Selected {
				t.Fatalf("%s pending unanimous: status = %v, want selected (never corroborated)", tc.name, decision.Status)
			}
			if len(decision.Support) != 1 {
				t.Fatalf("%s pending: support = %v, want exactly the first source", tc.name, decision.Support)
			}
		}
	})

	t.Run("pending dissent is not conflict", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			lp := policy.(rpc.LocalObservationPolicy)
			var first, second, third string
			switch tc.name {
			case "eth_getStorageAt":
				first, second, third = `"0x`+repeat00(32)+`"`, `"0x11`+repeat00(31)+`"`, `"0x22`+repeat00(31)+`"`
			case "eth_getCode", "eth_call":
				first, second, third = `"0x64"`, `"0x65"`, `"0x66"`
			default:
				first, second, third = `"0x64"`, `"0x65"`, `"0x66"`
			}
			decision, err := lp.ResolveLocal(pendingObs(first, second, third))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if decision.Status != rpc.Selected || string(decision.Value) != first {
				t.Fatalf("%s pending split: status = %v value = %s, want selected first answer", tc.name, decision.Status, decision.Value)
			}
		}
	})

	t.Run("pending without a usable source fails Unknown", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			lp := policy.(rpc.LocalObservationPolicy)
			failed := []rpc.Observation{
				{Source: rpc.SourceRef{Name: "a", ID: "a"}, Err: errors.New("down")},
				{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"not-hex"`)},
			}
			decision, err := lp.ResolveLocal(failed)
			if err == nil || !errors.Is(err, rpc.ErrNoValidResponse) {
				t.Fatalf("%s: got %v, want ErrNoValidResponse", tc.name, err)
			}
			if decision.Status != rpc.Unknown {
				t.Fatalf("%s: failed status = %v, want unknown", tc.name, decision.Status)
			}
		}
	})

	t.Run("pending keeps winner evidence", func(t *testing.T) {
		anchor := newTagAnchor(t)
		policy := rebuildSlicePolicy("eth_getBalance", anchor.fn)
		lp := policy.(rpc.LocalObservationPolicy)
		obs := pendingObs(`"0x64"`)
		obs[0].Evidence = json.RawMessage(`{"admitted":true}`)
		decision, err := lp.ResolveLocal(obs)
		if err != nil {
			t.Fatal(err)
		}
		if string(decision.Evidence) != `{"admitted":true}` {
			t.Fatalf("evidence = %s, want winner evidence", decision.Evidence)
		}
	})

	t.Run("pending eth_call keeps revert observations", func(t *testing.T) {
		anchor := newTagAnchor(t)
		policy := rebuildSlicePolicy("eth_call", anchor.fn)
		lp := policy.(rpc.LocalObservationPolicy)
		revert := &rpc.RPCError{Code: -32000, Message: "execution reverted"}
		obs := []rpc.Observation{{Source: rpc.SourceRef{Name: "a", ID: "a"}, Err: errors.New("execution reverted"), RPCError: revert}}
		decision, err := lp.ResolveLocal(obs)
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
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			var value string
			switch tc.name {
			case "eth_getStorageAt":
				value = `"0x` + repeat00(32) + `"`
			case "eth_getCode", "eth_call":
				value = `"0x1234"`
			default:
				value = `"0x64"`
			}
			decision, err := policy.Resolve(pendingObs(value, value))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if decision.Status != rpc.Corroborated {
				t.Fatalf("%s unanimous mined: status = %v, want corroborated", tc.name, decision.Status)
			}
		}
	})

	t.Run("capabilities survive both decorators", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			if _, ok := policy.(rpc.CorroboratingPolicy); !ok {
				t.Fatalf("%s lost CorroboratingPolicy", tc.name)
			}
			if _, ok := policy.(rpc.LocalObservationPolicy); !ok {
				t.Fatalf("%s lost LocalObservationPolicy", tc.name)
			}
			if ap, ok := policy.(rpc.AgreementPolicy); !ok || !ap.RequiresAgreement() {
				t.Fatalf("%s lost RequiresAgreement (mined-path adapter gate)", tc.name)
			}
			if _, ok := policy.(rpc.BlockRefProvider); !ok {
				t.Fatalf("%s lost BlockRefProvider", tc.name)
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
	heightCalls := 0
	height := func(ctx context.Context) (uint64, error) {
		heightCalls++
		return 16, nil
	}
	cases := []struct {
		name   string
		policy rpc.Policy
		params []any
		pos    int
	}{
		{"eth_createAccessList", CreateAccessListPolicy(nil), []any{map[string]any{"to": "0xabc"}, "pending"}, 1},
		{"eth_feeHistory", FeeHistoryPolicy(height), []any{uint64(4), "pending", []float64{}}, 1},
		{"eth_getBlockByNumber", BlockByNumberPolicy(), []any{"pending", false}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lp, ok := tc.policy.(rpc.LocalObservationPolicy)
			if !ok {
				t.Fatalf("%s does not expose LocalObservationPolicy", tc.name)
			}
			before := heightCalls
			prepared, err := tc.policy.Prepare(context.Background(), rpc.Request{Method: tc.name, Params: tc.params})
			if err != nil {
				t.Fatal(err)
			}
			if !lp.IsLocalObservation(prepared) {
				t.Fatalf("%s pending: not detected as pending-local", tc.name)
			}
			if heightCalls != before {
				t.Fatalf("%s pending consulted the anchor", tc.name)
			}
			if _, ok := tc.policy.(rpc.CorroboratingPolicy); !ok {
				t.Fatalf("%s lost CorroboratingPolicy", tc.name)
			}
		})
	}
}

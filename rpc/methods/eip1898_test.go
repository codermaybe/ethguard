package methods

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

const (
	testHashLatest    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testHashSafe      = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testHashFinalized = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testHashExplicit  = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

// slicePlans are the five default-block state reads whose EIP-1898 object
// form rides position 1 (position 2 for storage): the T02 first vertical
// slice, assembled through their compiled plans.
type slicePlan struct {
	name string
	plan rpc.ReadPlan
	pos  int
}

func slicePlans(t *testing.T) []slicePlan {
	t.Helper()
	return []slicePlan{
		{"eth_getBalance", compiledPlan(t, MethodGetBalance), 1},
		{"eth_getTransactionCount", compiledPlan(t, MethodGetTransactionCount), 1},
		{"eth_getCode", compiledPlan(t, MethodGetCode), 1},
		{"eth_getStorageAt", compiledPlan(t, MethodGetStorageAt), 2},
		{"eth_call", compiledPlan(t, MethodCall), 1},
	}
}

func withRef(prefix []any, pos int, ref any) []any {
	params := append([]any(nil), prefix...)
	for len(params) <= pos {
		params = append(params, nil)
	}
	params[pos] = ref
	return params
}

// sliceReference is the established evidence for one request: the fixture
// hash at height 16, corroborated, tagged with the level the request's
// requirement declares. Tests that need a specific level's hash pin it in
// the evidence they serve.
func sliceReference(t *testing.T, plan rpc.ReadPlan, req rpc.Request, hash string) (rpc.ReferenceEvidence, bool) {
	t.Helper()
	request, needed := plan.ReferenceRequirement(req)
	if !needed {
		return rpc.ReferenceEvidence{}, false
	}
	tag := string(request.Kind)
	if tag == string(rpc.ReferenceLatest) {
		tag = ""
	}
	return rpc.ReferenceEvidence{Hash: hash, Height: 16, Status: rpc.Corroborated, Tag: tag}, true
}

// prepareSliceRequest prepares one request through the plan's rewrite with
// the requested tag's hash as the established reference.
func prepareSliceRequest(t *testing.T, tc slicePlan, params []any, hash string) (rpc.Request, string, error) {
	t.Helper()
	req := rpc.Request{Method: tc.name, Params: params}
	if tc.plan.Validate != nil {
		if err := tc.plan.Validate(context.Background(), req); err != nil {
			return rpc.Request{}, "", err
		}
	}
	if tc.plan.PendingReject != nil {
		if err := tc.plan.PendingReject(req); err != nil {
			return rpc.Request{}, "", err
		}
	}
	ref, ok := sliceReference(t, tc.plan, req, hash)
	return tc.plan.PrepareWithReference(req, ref, ok)
}

// TestEIP1898BlockRefSlice covers the T02 first vertical slice: moving tags,
// explicit numbers/hashes, pending, EIP-1898 objects, requireCanonical, the
// tag-serving establishment contract, and local rejection of malformed
// requests — all through the compiled plans.
func TestEIP1898BlockRefSlice(t *testing.T) {
	t.Run("string moving tags rewrite to their own level's reference", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			for tag, want := range map[string]string{
				"latest": testHashLatest, "safe": testHashSafe, "finalized": testHashFinalized,
			} {
				prepared, _, err := prepareSliceRequest(t, tc, withRef([]any{"0xabc"}, tc.pos, tag), want)
				if err != nil {
					t.Fatalf("%s %s: %v", tc.name, tag, err)
				}
				if got := prepared.Params[tc.pos]; got != want {
					t.Fatalf("%s %s rewrote to %v, want %s (downgrade?)", tc.name, tag, got, want)
				}
			}
		}
	})

	t.Run("evidence for another level does not serve the request", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			req := rpc.Request{Method: tc.name, Params: withRef([]any{"0xabc"}, tc.pos, "safe")}
			if _, ok := sliceReference(t, tc.plan, req, testHashSafe); !ok {
				t.Fatalf("%s declared no reference need for safe", tc.name)
			}
			// The safe read is served the latest establishment: the evidence
			// carries the wrong level, so the rewrite refuses it.
			wrong := rpc.ReferenceEvidence{Hash: testHashSafe, Height: 16, Status: rpc.Corroborated, Tag: "latest"}
			if _, _, err := tc.plan.PrepareWithReference(req, wrong, true); !errors.Is(err, rpc.ErrReferenceRequired) {
				t.Fatalf("%s: safe read accepted latest evidence: %v", tc.name, err)
			}
		}
	})

	t.Run("explicit strings pass through without establishment", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			for _, ref := range []string{"0x10", testHashExplicit, "pending", "earliest", "0x0"} {
				// refOK=false: no establishment exists, and none is needed.
				prepared, _, err := prepareSliceRequest(t, tc, withRef([]any{"0xabc"}, tc.pos, ref), "")
				if err != nil {
					t.Fatalf("%s %v: %v", tc.name, ref, err)
				}
				if got := prepared.Params[tc.pos]; got != ref {
					t.Fatalf("%s %v rewritten to %v", tc.name, ref, got)
				}
			}
		}
	})

	t.Run("explicit hash objects pass through with requireCanonical preserved", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			for _, obj := range []map[string]any{
				{"blockHash": testHashExplicit},
				{"blockHash": testHashExplicit, "requireCanonical": true},
				{"blockHash": testHashExplicit, "requireCanonical": false},
			} {
				prepared, _, err := prepareSliceRequest(t, tc, withRef([]any{"0xabc"}, tc.pos, obj), "")
				if err != nil {
					t.Fatalf("%s %v: %v", tc.name, obj, err)
				}
				got, ok := prepared.Params[tc.pos].(map[string]any)
				if !ok || got["blockHash"] != testHashExplicit {
					t.Fatalf("%s %v rewritten to %v", tc.name, obj, prepared.Params[tc.pos])
				}
				if wantCanon, hasCanon := obj["requireCanonical"]; hasCanon && got["requireCanonical"] != wantCanon {
					t.Fatalf("%s requireCanonical dropped: %v", tc.name, got)
				}
			}
		}
	})

	t.Run("explicit number objects pass through with requireCanonical preserved", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			for _, obj := range []map[string]any{
				{"blockNumber": "0x10"},
				{"blockNumber": "0x10", "requireCanonical": true},
				{"blockNumber": "pending"},
				{"blockNumber": "earliest"},
			} {
				prepared, _, err := prepareSliceRequest(t, tc, withRef([]any{"0xabc"}, tc.pos, obj), "")
				if err != nil {
					t.Fatalf("%s %v: %v", tc.name, obj, err)
				}
				got, ok := prepared.Params[tc.pos].(map[string]any)
				if !ok || got["blockNumber"] != obj["blockNumber"] {
					t.Fatalf("%s %v rewritten to %v", tc.name, obj, prepared.Params[tc.pos])
				}
				if want, ok := obj["requireCanonical"]; ok && got["requireCanonical"] != want {
					t.Fatalf("%s requireCanonical dropped: %v", tc.name, got)
				}
			}
		}
	})

	t.Run("object moving tags rewrite to hash objects without downgrade", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			for tag, want := range map[string]string{
				"latest": testHashLatest, "safe": testHashSafe, "finalized": testHashFinalized,
			} {
				for _, canon := range []bool{false, true} {
					obj := map[string]any{"blockNumber": tag}
					if canon {
						obj["requireCanonical"] = true
					}
					prepared, _, err := prepareSliceRequest(t, tc, withRef([]any{"0xabc"}, tc.pos, obj), want)
					if err != nil {
						t.Fatalf("%s %v: %v", tc.name, obj, err)
					}
					got, ok := prepared.Params[tc.pos].(map[string]any)
					if !ok || got["blockHash"] != want {
						t.Fatalf("%s %v rewrote to %v, want blockHash %s", tc.name, obj, prepared.Params[tc.pos], want)
					}
					if _, exists := got["blockNumber"]; exists {
						t.Fatalf("%s %v kept blockNumber after pin: %v", tc.name, obj, got)
					}
					if canon && got["requireCanonical"] != true {
						t.Fatalf("%s requireCanonical dropped across rewrite: %v", tc.name, got)
					}
				}
			}
		}
	})

	t.Run("contradictory and malformed objects fail locally", func(t *testing.T) {
		bad := []map[string]any{
			{"blockHash": testHashExplicit, "blockNumber": "0x1"}, // tag+hash together
			{},                      // empty object
			{"blockHash": "0x1234"}, // short hash
			{"blockHash": "latest"}, // tag where a hash belongs
			{"blockHash": 1234},     // non-string hash
			{"blockNumber": 1234},   // non-string number
			{"blockNumber": "nope"}, // unknown tag, bad quantity
			{"blockNumber": "0x"},   // bad quantity
			{"blockHash": testHashExplicit, "requireCanonical": "yes"}, // non-bool flag
			{"blockHash": testHashExplicit, "extra": 1},                // unknown field
		}
		for _, tc := range slicePlans(t) {
			for _, obj := range bad {
				_, _, err := prepareSliceRequest(t, tc, withRef([]any{"0xabc"}, tc.pos, obj), testHashSafe)
				if err == nil || !errors.Is(err, rpc.ErrInvalidRequest) {
					t.Fatalf("%s %v: got %v, want ErrInvalidRequest", tc.name, obj, err)
				}
			}
		}
	})

	t.Run("missing establishment fails the request", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			for _, ref := range []any{"latest", map[string]any{"blockNumber": "finalized"}} {
				// refOK=false: the read declared a moving tag but no
				// established reference exists.
				req := rpc.Request{Method: tc.name, Params: withRef([]any{"0xabc"}, tc.pos, ref)}
				if _, _, err := tc.plan.PrepareWithReference(req, rpc.ReferenceEvidence{}, false); err == nil || !strings.Contains(err.Error(), "anchor") {
					t.Fatalf("%s %v: got %v, want an anchor failure", tc.name, ref, err)
				}
			}
		}
	})

	t.Run("the rewrite records the object pin", func(t *testing.T) {
		for _, tc := range slicePlans(t) {
			original := rpc.Request{Method: tc.name, Params: withRef([]any{"0xabc"}, tc.pos, map[string]any{"blockNumber": "safe"})}
			prepared, pin, err := prepareSliceRequest(t, tc, original.Params, testHashSafe)
			if err != nil {
				t.Fatal(err)
			}
			if pin != testHashSafe {
				t.Fatalf("%s pin = %v, want %s", tc.name, pin, testHashSafe)
			}
			explicit := rpc.Request{Method: tc.name, Params: withRef([]any{"0xabc"}, tc.pos, "0x10")}
			_, explicitPin, err := prepareSliceRequest(t, tc, explicit.Params, testHashSafe)
			if err != nil {
				t.Fatal(err)
			}
			if explicitPin != "" {
				t.Fatalf("%s explicit pin = %v, want empty", tc.name, explicitPin)
			}
			_ = prepared
		}
	})
}

// TestFeeHistoryRejectsHashInNewestBlock pins the quantity-only contract: a
// 32-byte hash must fail locally instead of riding the BlockNumber position
// to providers.
func TestFeeHistoryRejectsHashInNewestBlock(t *testing.T) {
	plan := compiledPlan(t, MethodFeeHistory)
	hash := "0x" + strings.Repeat("ab", 32)
	for _, newest := range []any{hash, map[string]any{"blockHash": hash}, map[string]any{"blockNumber": "0x10"}} {
		req := rpc.Request{Method: MethodFeeHistory, Params: []any{uint64(4), newest, []any{}}}
		if err := plan.Validate(context.Background(), req); err == nil || !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("newestBlock %v: got %v, want ErrInvalidRequest", newest, err)
		}
	}
}

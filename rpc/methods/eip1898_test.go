package methods

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ethguard/rpc"
)

const (
	testHashLatest    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testHashSafe      = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testHashFinalized = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testHashExplicit  = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

type tagAnchor struct {
	t      *testing.T
	seen   []string
	fail   bool
	hashes map[string]string
}

func (a *tagAnchor) fn(ctx context.Context) (string, error) {
	tag := rpc.AnchorTag(ctx)
	a.seen = append(a.seen, tag)
	if a.fail {
		return "", errors.New("anchor down")
	}
	if h, ok := a.hashes[tag]; ok {
		return h, nil
	}
	a.t.Fatalf("anchor called for unexpected tag %q", tag)
	return "", nil
}

func newTagAnchor(t *testing.T) *tagAnchor {
	t.Helper()
	return &tagAnchor{t: t, hashes: map[string]string{
		"latest":    testHashLatest,
		"safe":      testHashSafe,
		"finalized": testHashFinalized,
	}}
}

func slicePolicies(anchor rpc.AnchorFunc) []struct {
	name   string
	policy rpc.Policy
	prefix []any
	pos    int
} {
	return []struct {
		name   string
		policy rpc.Policy
		prefix []any
		pos    int
	}{
		{"eth_getBalance", GetBalancePolicy(anchor), []any{"0xabc"}, 1},
		{"eth_getTransactionCount", GetTransactionCountPolicy(anchor), []any{"0xabc"}, 1},
		{"eth_getCode", GetCodePolicy(anchor), []any{"0xabc"}, 1},
		{"eth_getStorageAt", GetStorageAtPolicy(anchor), []any{"0xabc", "0x01"}, 2},
		{"eth_call", CallPolicy(anchor), []any{map[string]any{"to": "0xabc"}}, 1},
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

// TestEIP1898BlockRefSlice covers the T02 first vertical slice: moving tags,
// explicit numbers/hashes, pending, EIP-1898 objects, requireCanonical, the
// exact anchor interaction, and local rejection of malformed requests.
func TestEIP1898BlockRefSlice(t *testing.T) {
	t.Run("string moving tags rewrite tag-aware without downgrade", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for tag, want := range map[string]string{
				"latest": testHashLatest, "safe": testHashSafe, "finalized": testHashFinalized,
			} {
				anchor.seen = nil
				prepared, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, tag),
				})
				if err != nil {
					t.Fatalf("%s %s: %v", tc.name, tag, err)
				}
				if got := prepared.Params[tc.pos]; got != want {
					t.Fatalf("%s %s rewrote to %v, want %s (downgrade?)", tc.name, tag, got, want)
				}
				if len(anchor.seen) != 1 || anchor.seen[0] != tag {
					t.Fatalf("%s %s: anchor saw %v, want [%s]", tc.name, tag, anchor.seen, tag)
				}
			}
		}
	})

	t.Run("explicit strings pass through without anchor", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for _, ref := range []string{"0x10", testHashExplicit, "pending", "earliest", "0x0"} {
				anchor.seen = nil
				prepared, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, ref),
				})
				if err != nil {
					t.Fatalf("%s %v: %v", tc.name, ref, err)
				}
				if got := prepared.Params[tc.pos]; got != ref {
					t.Fatalf("%s %v rewritten to %v", tc.name, ref, got)
				}
				if len(anchor.seen) != 0 {
					t.Fatalf("%s %v consulted anchor: %v", tc.name, ref, anchor.seen)
				}
			}
		}
	})

	t.Run("explicit hash objects pass through with requireCanonical preserved", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for _, obj := range []map[string]any{
				{"blockHash": testHashExplicit},
				{"blockHash": testHashExplicit, "requireCanonical": true},
				{"blockHash": testHashExplicit, "requireCanonical": false},
			} {
				anchor.seen = nil
				prepared, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, obj),
				})
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
				if len(anchor.seen) != 0 {
					t.Fatalf("%s %v consulted anchor", tc.name, obj)
				}
			}
		}
	})

	t.Run("explicit number objects pass through with requireCanonical preserved", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for _, obj := range []map[string]any{
				{"blockNumber": "0x10"},
				{"blockNumber": "0x10", "requireCanonical": true},
				{"blockNumber": "pending"},
				{"blockNumber": "earliest"},
			} {
				anchor.seen = nil
				prepared, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, obj),
				})
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
				if len(anchor.seen) != 0 {
					t.Fatalf("%s %v consulted anchor", tc.name, obj)
				}
			}
		}
	})

	t.Run("object moving tags rewrite to hash objects without downgrade", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for tag, want := range map[string]string{
				"latest": testHashLatest, "safe": testHashSafe, "finalized": testHashFinalized,
			} {
				for _, canon := range []bool{false, true} {
					anchor.seen = nil
					obj := map[string]any{"blockNumber": tag}
					if canon {
						obj["requireCanonical"] = true
					}
					prepared, err := policy.Prepare(context.Background(), rpc.Request{
						Method: tc.name, Params: withRef(tc.prefix, tc.pos, obj),
					})
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
					if len(anchor.seen) != 1 || anchor.seen[0] != tag {
						t.Fatalf("%s %v: anchor saw %v, want [%s]", tc.name, obj, anchor.seen, tag)
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
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for _, obj := range bad {
				anchor.seen = nil
				_, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, obj),
				})
				if err == nil || !errors.Is(err, rpc.ErrInvalidRequest) {
					t.Fatalf("%s %v: got %v, want ErrInvalidRequest", tc.name, obj, err)
				}
				if len(anchor.seen) != 0 {
					t.Fatalf("%s %v consulted anchor before failing", tc.name, obj)
				}
			}
		}
	})

	t.Run("anchor failure fails the request", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			anchor.fail = true
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			for _, ref := range []any{"latest", map[string]any{"blockNumber": "finalized"}} {
				_, err := policy.Prepare(context.Background(), rpc.Request{
					Method: tc.name, Params: withRef(tc.prefix, tc.pos, ref),
				})
				if err == nil || !strings.Contains(err.Error(), "anchor") {
					t.Fatalf("%s %v: got %v, want anchor failure", tc.name, ref, err)
				}
			}
		}
	})

	t.Run("BlockRef reports object pins", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			original := rpc.Request{Method: tc.name, Params: withRef(tc.prefix, tc.pos, map[string]any{"blockNumber": "safe"})}
			prepared, err := policy.Prepare(context.Background(), original)
			if err != nil {
				t.Fatal(err)
			}
			provider, ok := policy.(rpc.BlockRefProvider)
			if !ok {
				t.Fatalf("%s does not report BlockRef", tc.name)
			}
			if got := provider.BlockRef(original, prepared); got != testHashSafe {
				t.Fatalf("%s BlockRef = %v, want %s", tc.name, got, testHashSafe)
			}
			explicit := rpc.Request{Method: tc.name, Params: withRef(tc.prefix, tc.pos, "0x10")}
			preparedExplicit, err := policy.Prepare(context.Background(), explicit)
			if err != nil {
				t.Fatal(err)
			}
			if got := provider.BlockRef(explicit, preparedExplicit); got != "" {
				t.Fatalf("%s explicit BlockRef = %v, want empty", tc.name, got)
			}
		}
	})

	t.Run("corroborating capability survives the decorator", func(t *testing.T) {
		for _, tc := range slicePolicies(nil) {
			anchor := newTagAnchor(t)
			policy := rebuildSlicePolicy(tc.name, anchor.fn)
			if _, ok := policy.(rpc.CorroboratingPolicy); !ok {
				t.Fatalf("%s lost CorroboratingPolicy across EIP1898BlockRef", tc.name)
			}
		}
	})
}

func rebuildSlicePolicy(name string, anchor rpc.AnchorFunc) rpc.Policy {
	switch name {
	case "eth_getBalance":
		return GetBalancePolicy(anchor)
	case "eth_getTransactionCount":
		return GetTransactionCountPolicy(anchor)
	case "eth_getCode":
		return GetCodePolicy(anchor)
	case "eth_getStorageAt":
		return GetStorageAtPolicy(anchor)
	case "eth_call":
		return CallPolicy(anchor)
	}
	panic("unknown slice method " + name)
}

// TestFeeHistoryRejectsHashInNewestBlock pins the quantity-only contract: a
// 32-byte hash must fail locally instead of riding the BlockNumber position
// to providers.
func TestFeeHistoryRejectsHashInNewestBlock(t *testing.T) {
	policy := FeeHistoryPolicy(func(ctx context.Context) (uint64, error) { return 1, nil })
	hash := "0x" + strings.Repeat("ab", 32)
	for _, newest := range []any{hash, map[string]any{"blockHash": hash}, map[string]any{"blockNumber": "0x10"}} {
		_, err := policy.Prepare(context.Background(), rpc.Request{
			Method: MethodFeeHistory, Params: []any{uint64(4), newest, []any{}},
		})
		if err == nil || !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("newestBlock %v: got %v, want ErrInvalidRequest", newest, err)
		}
	}
}

package methods

import (
	"context"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

const testReceiptsAnchorHash = "0x1111111111111111111111111111111111111111111111111111111111111111"

// TestBlockReceiptsMovingTagObjectAnchorsLikeTheMatrix pins the
// eth_getBlockReceipts matrix row for EIP-1898 objects: a moving-tag object
// declares a reference need and is rewritten to the anchor hash, exactly
// like the string form. Without the EIP1898 declaration the object form
// silently skips anchoring and fails after fan-out at the admission layer.
func TestBlockReceiptsMovingTagObjectAnchorsLikeTheMatrix(t *testing.T) {
	plan := compiledPlan(t, MethodGetBlockReceipts)
	req := rpc.Request{Method: MethodGetBlockReceipts, Params: []any{map[string]any{"blockNumber": "latest"}}}
	if err := plan.Validate(context.Background(), req); err != nil {
		t.Fatalf("a moving-tag object without requireCanonical is a legal request: %v", err)
	}
	if _, needed := plan.ReferenceRequirement(req); !needed {
		t.Fatal("moving-tag object must declare a reference need")
	}
	evidence := rpc.ReferenceEvidence{Hash: testReceiptsAnchorHash, Height: 16, Status: rpc.Corroborated, Tag: "latest"}
	prepared, pin, err := plan.PrepareWithReference(req, evidence, true)
	if err != nil {
		t.Fatalf("corroborated latest evidence must serve the rewrite: %v", err)
	}
	if pin != testReceiptsAnchorHash {
		t.Fatalf("rewrite pin = %q, want the anchor hash", pin)
	}
	obj, ok := prepared.Params[0].(map[string]any)
	if !ok || obj["blockHash"] != testReceiptsAnchorHash {
		t.Fatalf("moving tag not rewritten to the anchor hash: %#v", prepared.Params[0])
	}
	if _, stillTag := obj["blockNumber"]; stillTag {
		t.Fatalf("rewritten object still carries the moving tag: %#v", obj)
	}
}

// TestBlockReceiptsRequireCanonicalWithMovingTagRejectedLocally pins the
// requireCanonical column of the eth_getBlockReceipts matrix row:
// requireCanonical combined with a moving tag is rejected locally —
// ErrInvalidRequest before any establishment or provider contact — never
// silently dropped and never resolved after fan-out.
func TestBlockReceiptsRequireCanonicalWithMovingTagRejectedLocally(t *testing.T) {
	plan := compiledPlan(t, MethodGetBlockReceipts)
	for _, obj := range []map[string]any{
		{"blockNumber": "latest", "requireCanonical": true},
		{"blockNumber": "safe", "requireCanonical": true},
		{"blockNumber": "finalized", "requireCanonical": true},
	} {
		req := rpc.Request{Method: MethodGetBlockReceipts, Params: []any{obj}}
		err := plan.Validate(context.Background(), req)
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("%v: requireCanonical with a moving tag must be rejected locally, got %v", obj, err)
		}
		// The pipeline runs Validate before ReferenceRequirement and
		// Establish (aggregator/phases.go), so this local rejection is what
		// the read hits first: no anchor is ever established for it. The
		// requirement derivation itself stays method-blind on purpose —
		// getBalance rewrites the same object instead of rejecting it.
	}
}

// TestBlockReceiptsExplicitObjectPassesThroughWithRequireCanonical pins the
// preserved half of the same column: explicit height and hash objects pass
// through untouched, requireCanonical included.
func TestBlockReceiptsExplicitObjectPassesThroughWithRequireCanonical(t *testing.T) {
	plan := compiledPlan(t, MethodGetBlockReceipts)
	for _, obj := range []map[string]any{
		{"blockNumber": "0x10"},
		{"blockNumber": "0x10", "requireCanonical": true},
		{"blockHash": testReceiptsAnchorHash, "requireCanonical": true},
		{"blockNumber": "earliest", "requireCanonical": true},
	} {
		req := rpc.Request{Method: MethodGetBlockReceipts, Params: []any{obj}}
		if err := plan.Validate(context.Background(), req); err != nil {
			t.Fatalf("%v: explicit object must pass request validation: %v", obj, err)
		}
		if _, needed := plan.ReferenceRequirement(req); needed {
			t.Fatalf("%v: explicit object must not need a reference", obj)
		}
		prepared, _, err := plan.PrepareWithReference(req, rpc.ReferenceEvidence{}, false)
		if err != nil {
			t.Fatalf("%v: pass-through prepare failed: %v", obj, err)
		}
		got, ok := prepared.Params[0].(map[string]any)
		if !ok || got["blockNumber"] != obj["blockNumber"] || got["blockHash"] != obj["blockHash"] {
			t.Fatalf("%v: explicit object rewritten: %#v", obj, prepared.Params[0])
		}
		if want, has := obj["requireCanonical"]; has && got["requireCanonical"] != want {
			t.Fatalf("%v: requireCanonical dropped: %#v", obj, got)
		}
	}
}

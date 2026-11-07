package rpc

import (
	"errors"
	"testing"
)

// TestEIP1898DeclarationWithoutPositionsNeverPanics pins the defensive
// contract on hand-assembled plans: the public ReadPlan struct allows an
// EIP1898 declaration with no positions (the field docs describe the zero
// spec as "never needs a reference", which invites exactly this assembly),
// and both requirement derivation and the prepare rewrite must degrade to
// not-needed instead of indexing into the empty position list. CompilePlan
// rejects the combination for compiled plans; these seams own the runtime
// half of the guard.
func TestEIP1898DeclarationWithoutPositionsNeverPanics(t *testing.T) {
	plan := ReadPlan{Reference: ReferenceSpec{EIP1898: true}}
	req := Request{Method: "eth_custom", Params: []any{"a", map[string]any{"blockNumber": "latest"}}}

	if _, needed := plan.ReferenceRequirement(req); needed {
		t.Fatal("an EIP1898 declaration without positions cannot serve a reference")
	}
	prepared, pin, err := plan.PrepareWithReference(req, ReferenceEvidence{}, false)
	if err != nil {
		t.Fatalf("prepare without positions must pass the request through: %v", err)
	}
	if pin != "" {
		t.Fatalf("no reference was needed, got pin %q", pin)
	}
	if len(prepared.Params) != 2 || prepared.Params[0] != "a" {
		t.Fatalf("pass-through must preserve the parameter list: %#v", prepared.Params)
	}
}

// TestFieldsRequirementMatchesRewriteStrictness pins the requirement
// derivation to the rewrite's own grammar: contradictory explicit tags
// decline to establish in both orders (the rewrite owns the local rejection,
// with no wasted anchor round), and a missing field inherits the explicit
// tags' level instead of forcing a latest default.
func TestFieldsRequirementMatchesRewriteStrictness(t *testing.T) {
	plan := ReadPlan{Reference: ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}}}
	for _, obj := range []map[string]any{
		{"fromBlock": "latest", "toBlock": "safe"},
		{"fromBlock": "safe", "toBlock": "latest"},
	} {
		req := Request{Params: []any{obj}}
		if _, needed := plan.ReferenceRequirement(req); needed {
			t.Fatalf("%v: contradictory tags must not establish a reference", obj)
		}
		_, _, err := plan.PrepareWithReference(req, ReferenceEvidence{}, false)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%v: rewrite must own the mixed-tags rejection, got %v", obj, err)
		}
	}
	reqr, needed := plan.ReferenceRequirement(Request{Params: []any{map[string]any{"fromBlock": "safe"}}})
	if !needed || reqr.Kind != ReferenceKindForTag("safe") {
		t.Fatalf("safe-only read requirement = %+v needed=%v, want the safe anchor", reqr, needed)
	}
	reqr, needed = plan.ReferenceRequirement(Request{Params: []any{map[string]any{}}})
	if !needed || reqr.Kind != ReferenceKindForTag("latest") {
		t.Fatalf("all-implicit read requirement = %+v needed=%v, want latest", reqr, needed)
	}
}

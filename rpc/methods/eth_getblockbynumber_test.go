package methods

import (
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// TestBlockByNumberPendingTagIsNotRewrittenToAnchor locks the policy half of
// T03-A: the pending tag stays a local observation and is not rewritten into
// an anchored block read. Typed rejection of -1 lives in the adapter.
func TestBlockByNumberPendingTagIsNotRewrittenToAnchor(t *testing.T) {
	plan := compiledPlan(t, MethodGetBlockByNumber)
	original := rpc.Request{Method: MethodGetBlockByNumber, Params: []any{"PeNdInG", true}}
	// The pending tag is observed, never rewritten: no reference need, no
	// pin, and the request rides the fan-out untouched.
	if _, needed := plan.ReferenceRequirement(original); needed {
		t.Fatal("pending tag declared a reference need")
	}
	evidence, needed := establishedEvidence(t, plan, original)
	if needed {
		t.Fatal("pending tag established a reference")
	}
	prepared, pin, err := plan.PrepareWithReference(original, evidence, false)
	if err != nil {
		t.Fatalf("pending tag rejected by plan: %v", err)
	}
	if len(prepared.Params) != 2 || prepared.Params[0] != "PeNdInG" || prepared.Params[1] != true {
		t.Fatalf("pending tag rewritten: %+v", prepared.Params)
	}
	if pin != "" {
		t.Fatalf("pending tag invented anchor pin %q", pin)
	}
	if plan.Pending.Mode != rpc.PendingObserve || plan.Pending.Pos != 0 {
		t.Fatalf("pending declaration = %+v, want observe at 0", plan.Pending)
	}
}

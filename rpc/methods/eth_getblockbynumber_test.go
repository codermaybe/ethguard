package methods

import (
	"context"
	"testing"

	"ethguard/rpc"
)

// TestBlockByNumberPendingTagIsNotRewrittenToAnchor locks the policy half of
// T03-A: the pending tag stays a local observation and is not rewritten into
// an anchored block read. Typed rejection of -1 lives in the adapter.
func TestBlockByNumberPendingTagIsNotRewrittenToAnchor(t *testing.T) {
	policy := BlockByNumberPolicy()
	original := rpc.Request{Method: MethodGetBlockByNumber, Params: []any{"PeNdInG", true}}
	prepared, err := policy.Prepare(context.Background(), original)
	if err != nil {
		t.Fatalf("pending tag rejected by policy: %v", err)
	}
	if len(prepared.Params) != 2 || prepared.Params[0] != "PeNdInG" || prepared.Params[1] != true {
		t.Fatalf("pending tag rewritten: %+v", prepared.Params)
	}
	local, ok := policy.(rpc.LocalObservationPolicy)
	if !ok || !local.IsLocalObservation(prepared) {
		t.Fatal("pending tag was not kept as a local observation")
	}
	if ref := policy.(rpc.BlockRefProvider).BlockRef(original, prepared); ref != "" {
		t.Fatalf("pending tag invented anchor ref %q", ref)
	}
}

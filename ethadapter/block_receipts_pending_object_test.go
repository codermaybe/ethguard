package ethadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// TestBlockReceiptsPendingObjectSendsNoUpstreamRequest locks choice (a):
// a generic Call may pass an EIP-1898 pending object that the typed adapter
// cannot build. RejectPending fails that shape locally, before anchor
// establishment or eth_getBlockReceipts fan-out.
func TestBlockReceiptsPendingObjectSendsNoUpstreamRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{"number": "0x10", "hash": "0xabc"})
		node.Return(methods.MethodGetBlockReceipts, []any{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	refs := []any{
		map[string]any{"blockNumber": "pending"},
		map[string]any{"blockNumber": "PENDING", "requireCanonical": true},
		json.RawMessage(`{"blockNumber":"Pending"}`),
		json.RawMessage(`"pending"`),
		" pending ",
		"pending",
	}
	for _, ref := range refs {
		decision, err := client.Unwrap().Call(ctx, methods.MethodGetBlockReceipts, ref)
		if !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("%v: got %v, want ErrInvalidRequest", ref, err)
		}
		if decision.Status != rpc.Unknown || len(decision.Support) != 0 {
			t.Fatalf("%v: local rejection became a business decision: %+v", ref, decision)
		}
		if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) || errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("%v: pending rejection swallowed by corroboration: %v", ref, err)
		}
		if !strings.Contains(err.Error(), "pending is not supported") {
			t.Fatalf("%v: error %v must say pending is unsupported", ref, err)
		}
	}
	for _, node := range nodes {
		if calls := node.Calls(); len(calls) != 0 {
			t.Fatalf("pending block receipts contacted upstream: %+v", calls)
		}
	}
}

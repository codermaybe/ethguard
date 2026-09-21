package ethguard_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

// TestPendingExtendedMethodsAreSelectedNotCorroborated extends R1 past the
// first slice: block-by-number, fee history, and access-list creation serve
// pending as node-local observations. Unanimous pending agreement stays
// Selected; nothing is rewritten and no anchor is consulted.
func TestPendingExtendedMethodsAreSelectedNotCorroborated(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	pendingBlock := fixtureBlock(t, 16)
	feePayload := `{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`
	listPayload := `{"accessList":[],"gasUsed":"0x5208"}`
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			return json.RawMessage(pendingBlock), nil
		})
		node.Return(methods.MethodFeeHistory, json.RawMessage(feePayload))
		node.Return(methods.MethodCreateAccessList, json.RawMessage(listPayload))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	byNumber, err := client.Call(ctx, methods.MethodGetBlockByNumber, "pending", true)
	if err != nil {
		t.Fatalf("pending byNumber: %v", err)
	}
	if byNumber.Status != rpc.Selected {
		t.Fatalf("pending byNumber: status = %v, want selected", byNumber.Status)
	}
	fee, err := client.Call(ctx, methods.MethodFeeHistory, uint64(1), "pending", []float64{})
	if err != nil {
		t.Fatalf("pending feeHistory: %v", err)
	}
	if fee.Status != rpc.Selected {
		t.Fatalf("pending feeHistory: status = %v, want selected", fee.Status)
	}
	list, err := client.Call(ctx, methods.MethodCreateAccessList, map[string]any{"to": "0xdad"}, "pending")
	if err != nil {
		t.Fatalf("pending accessList: %v", err)
	}
	if list.Status != rpc.Selected {
		t.Fatalf("pending accessList: status = %v, want selected", list.Status)
	}
	for _, decision := range []rpc.Decision{byNumber, fee, list} {
		if len(decision.Support) != 1 {
			t.Fatalf("pending support = %+v, want exactly the first source", decision.Support)
		}
	}
	for _, node := range nodes {
		for _, c := range node.Calls() {
			if c.Method != methods.MethodGetBlockByNumber {
				continue
			}
			var fullTx bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &fullTx)
			}
			if !fullTx {
				t.Fatal("pending consulted the anchor")
			}
		}
	}
}

package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestEstimateAndAccessListObjectsOnTheWire(t *testing.T) {
	call := map[string]any{"to": "0x0000000000000000000000000000000000000001", "data": "0x18160ddd"}
	access := map[string]any{"accessList": []any{}, "gasUsed": "0x5208"}

	t.Run("moving tag object rewrites to a hash object", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		agreeOnHead(nodes)
		for _, node := range nodes {
			node.Handle(methods.MethodEstimateGas, func(c rpctest.Call) (any, error) {
				if objectBlockHash(c) == "0xabc" {
					return "0x66", nil
				}
				return nil, rpctest.RPCError(-32602, "object was not pinned")
			})
			node.Handle(methods.MethodCreateAccessList, func(c rpctest.Call) (any, error) {
				if objectBlockHash(c) == "0xabc" {
					return access, nil
				}
				return nil, rpctest.RPCError(-32602, "object was not pinned")
			})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		estimate, err := client.Call(ctx, methods.MethodEstimateGas, call, map[string]any{"blockNumber": "latest", "requireCanonical": true})
		if err != nil || estimate.Status != rpc.Selected || string(estimate.Value) != `"0x66"` {
			t.Fatalf("estimate = %+v %v", estimate, err)
		}
		list, err := client.Call(ctx, methods.MethodCreateAccessList, call, map[string]any{"blockNumber": "safe"})
		if err != nil || list.Status != rpc.Corroborated {
			t.Fatalf("access list = %+v %v", list, err)
		}
		for _, node := range nodes {
			for _, seen := range node.Calls() {
				if seen.Method != methods.MethodEstimateGas && seen.Method != methods.MethodCreateAccessList {
					continue
				}
				var obj map[string]any
				if len(seen.Params) != 2 || json.Unmarshal(seen.Params[1], &obj) != nil || obj["blockHash"] != "0xabc" {
					t.Fatalf("%s wire = %s", seen.Method, seen.Params)
				}
				if _, ok := obj["blockNumber"]; ok {
					t.Fatalf("%s kept the moving tag: %s", seen.Method, seen.Params[1])
				}
			}
		}
	})

	t.Run("pending object is not anchored", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		for _, node := range nodes {
			node.Return(methods.MethodEstimateGas, "0x10")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		decision, err := client.Call(ctx, methods.MethodEstimateGas, call, map[string]any{"blockNumber": "pending"})
		if err != nil || decision.Status != rpc.Selected {
			t.Fatalf("pending estimate = %+v %v", decision, err)
		}
		for _, node := range nodes {
			for _, seen := range node.Calls() {
				if seen.Method == methods.MethodGetBlockByNumber || seen.Method == methods.MethodGetBlockByHash {
					t.Fatalf("pending estimate anchored via %s", seen.Method)
				}
				if seen.Method == methods.MethodEstimateGas {
					var obj map[string]any
					if json.Unmarshal(seen.Params[1], &obj) != nil || obj["blockNumber"] != "pending" {
						t.Fatalf("pending object rewritten: %s", seen.Params)
					}
				}
			}
		}
	})

	t.Run("invalid object sends nothing", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 2)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		decision, err := client.Call(ctx, methods.MethodCreateAccessList, call, map[string]any{
			"blockHash": "0x" + "ab", "blockNumber": "latest",
		})
		if !errors.Is(err, ethguard.ErrInvalidRequest) || decision.Status != rpc.Unknown {
			t.Fatalf("invalid = %+v %v", decision, err)
		}
		for _, node := range nodes {
			if calls := node.Calls(); len(calls) != 0 {
				t.Fatalf("invalid object contacted upstream: %+v", calls)
			}
		}
	})

	t.Run("provider errors and payload conflict stay unknown", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 2)
		agreeOnHead(nodes)
		for _, node := range nodes {
			node.Fail(methods.MethodEstimateGas, -32000, "execution reverted")
		}
		nodes[0].Return(methods.MethodCreateAccessList, access)
		nodes[1].Return(methods.MethodCreateAccessList, map[string]any{"accessList": []any{}, "gasUsed": "0x5209"})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		estimate, err := client.Call(ctx, methods.MethodEstimateGas, call, map[string]any{"blockNumber": "latest"})
		if !errors.Is(err, ethguard.ErrNoValidResponse) || estimate.Status != rpc.Unknown {
			t.Fatalf("provider error = %+v %v", estimate, err)
		}
		if errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("provider error swallowed as conflict: %v", err)
		}
		list, err := client.Call(ctx, methods.MethodCreateAccessList, call, "0x10")
		if !errors.Is(err, ethguard.ErrConflict) || list.Status != rpc.Unknown {
			t.Fatalf("conflict = %+v %v", list, err)
		}
	})
}

func objectBlockHash(c rpctest.Call) string {
	if len(c.Params) != 2 {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(c.Params[1], &obj) != nil {
		return ""
	}
	hash, _ := obj["blockHash"].(string)
	return hash
}

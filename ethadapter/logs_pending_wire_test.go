package ethadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// TestLogsPendingRangeSendsNoProviderRequest is the wire lock for T03-A:
// a pending log filter is ErrInvalidRequest before anchor establishment or
// eth_getLogs fan-out, even when every provider would answer.
func TestLogsPendingRangeSendsNoProviderRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{"number": "0x10", "hash": "0xabc"})
		node.Return(methods.MethodGetLogs, []any{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filters := []any{
		map[string]any{"fromBlock": "pending"},
		map[string]any{"toBlock": "PENDING"},
		map[string]any{"fromBlock": "Pending", "toBlock": "pending"},
		map[string]any{"address": "0x0000000000000000000000000000000000000001", "fromBlock": "pending", "topics": []any{}},
		map[string]any{"blockHash": "pending"},
		map[string]any{"blockHash": "0xabc", "fromBlock": "pending"},
		map[string]any{"fromBlock": json.RawMessage(`"pending"`)},
		map[string]any{"fromBlock": 1},
		map[string]any{"toBlock": nil},
		map[string]any{"fromBlock": ""},
	}
	for _, filter := range filters {
		decision, err := client.Unwrap().Call(ctx, methods.MethodGetLogs, filter)
		if !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("filter %v: got %v, want ErrInvalidRequest", filter, err)
		}
		if decision.Status != rpc.Unknown || len(decision.Support) != 0 {
			t.Fatalf("filter %v: local rejection became a business decision: %+v", filter, decision)
		}
		if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) || errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("filter %v: pending rejection swallowed by corroboration: %v", filter, err)
		}
		pendingFilter := filterMentionsPending(filter)
		if pendingFilter && !strings.Contains(err.Error(), "no stable corroboration semantics") {
			t.Fatalf("filter %v: error %v must name the missing pending-range corroboration semantics", filter, err)
		}
	}

	_, err := client.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: big.NewInt(-1)})
	if !errors.Is(err, ethguard.ErrInvalidRequest) || !strings.Contains(err.Error(), "no stable corroboration semantics") {
		t.Fatalf("FilterLogs(-1): got %v, want local pending-range rejection", err)
	}
	_, err = client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(1),
		ToBlock:   big.NewInt(-1),
		Addresses: []common.Address{common.HexToAddress("0x1")},
	})
	if !errors.Is(err, ethguard.ErrInvalidRequest) || !strings.Contains(err.Error(), "no stable corroboration semantics") {
		t.Fatalf("FilterLogs mixed pending: got %v, want local pending-range rejection", err)
	}

	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetLogs || call.Method == methods.MethodGetBlockByNumber || call.Method == methods.MethodGetBlockByHash {
				t.Fatalf("pending logs contacted %s: %s", call.Method, call.Params)
			}
		}
	}
}

func filterMentionsPending(filter any) bool {
	encoded, err := json.Marshal(filter)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(encoded)), "pending")
}

package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
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
		decision, err := callDecision(client.Call(ctx, methods.MethodGetLogs, filter))
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

	// The generated FilterQuery grammar spells range bounds as canonical
	// quantities or the earliest/latest/safe/finalized tags, so a pending
	// bound cannot be expressed through the parser; it rides the union
	// directly and is rejected locally before any contact, like the bare-tag
	// form the generic Call filters above already pin.
	pendingTag := ethapi.Text("pending")
	one := mustQuantity(t, 1)
	address := ethapi.Address("0x0000000000000000000000000000000000000001")
	pendingQueries := map[string]ethapi.FilterQuery{
		"pending": {FilterByBlockRange: &ethapi.FilterQueryRange{FromBlock: &ethapi.FilterBlock{BlockTag: &pendingTag}}},
		"mixed-pending": {FilterByBlockRange: &ethapi.FilterQueryRange{
			FromBlock: &ethapi.FilterBlock{BlockNumber: &one},
			ToBlock:   &ethapi.FilterBlock{BlockTag: &pendingTag},
			Address:   &ethapi.FilterAddress{Address: &address},
		}},
	}
	for name, query := range pendingQueries {
		_, _, filterErr := client.FilterLogs(ctx, query)
		if !errors.Is(filterErr, ethguard.ErrInvalidRequest) {
			t.Fatalf("FilterLogs %s: got %v, want local ErrInvalidRequest", name, filterErr)
		}
	}
	reversed, reversedErr := ethapi.NewFilterQuery(json.RawMessage(`{"fromBlock":"0x10","toBlock":"0xf"}`))
	if reversedErr != nil {
		t.Fatal(reversedErr)
	}
	if _, _, filterErr := client.FilterLogs(ctx, reversed); !errors.Is(filterErr, ethguard.ErrInvalidRequest) || !strings.Contains(filterErr.Error(), "fromBlock is greater than toBlock") {
		t.Fatalf("FilterLogs reversed range: got %v, want local ErrInvalidRequest", filterErr)
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

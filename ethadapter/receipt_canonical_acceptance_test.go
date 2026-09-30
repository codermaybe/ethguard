package ethadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"ethguard"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

func receiptLogField(t *testing.T, raw json.RawMessage, field string, value json.RawMessage) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	var logs []map[string]json.RawMessage
	if err := json.Unmarshal(obj["logs"], &logs); err != nil {
		t.Fatal(err)
	}
	if value == nil {
		delete(logs[0], field)
	} else {
		logs[0][field] = value
	}
	data, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	obj["logs"] = data
	data, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCanonicalReceiptLogTimestampThroughAdapter(t *testing.T) {
	for _, timestamp := range []struct {
		name    string
		raw     json.RawMessage
		want    uint64
		encoded string
	}{
		{"present", json.RawMessage(`"0xAB"`), 171, `"blockTimestamp":"0xab"`},
		{"missing", nil, 0, ""},
		{"null", json.RawMessage(`null`), 0, ""},
	} {
		t.Run(timestamp.name, func(t *testing.T) {
			client, nodes := fleet(t, 3)
			hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
			raw := receiptLogField(t, fixtureReceipt(t, hash, 16), "blockTimestamp", timestamp.raw)
			for _, node := range nodes {
				node.Return(methods.MethodGetTransactionReceipt, raw)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := client.TransactionReceipt(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			if result.Logs[0].BlockTimestamp != timestamp.want {
				t.Fatalf("timestamp = %d", result.Logs[0].BlockTimestamp)
			}
			decision, err := client.Unwrap().Call(ctx, methods.MethodGetTransactionReceipt, hash.Hex())
			if err != nil {
				t.Fatal(err)
			}
			if timestamp.encoded == "" {
				if bytes.Contains(decision.Value, []byte(`"blockTimestamp"`)) {
					t.Fatalf("value = %s", decision.Value)
				}
			} else if !bytes.Contains(decision.Value, []byte(timestamp.encoded)) {
				t.Fatalf("value = %s", decision.Value)
			}
		})
	}
}

func TestCanonicalReceiptTimestampDisagreementIsConflict(t *testing.T) {
	client, nodes := fleet(t, 2, ethguard.WithQuorum(1))
	hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
	for i, node := range nodes {
		timestamp := json.RawMessage(`"0x1"`)
		if i == 1 {
			timestamp = json.RawMessage(`"0x2"`)
		}
		node.Return(methods.MethodGetTransactionReceipt, receiptLogField(t, fixtureReceipt(t, hash, 16), "blockTimestamp", timestamp))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decision, err := client.Unwrap().Call(ctx, methods.MethodGetTransactionReceipt, hash.Hex())
	if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown || len(decision.Support) != 1 || len(decision.Reject) != 1 {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	// The protected timestamp remains in the selected cluster's diagnostic Value.
	if !bytes.Contains(decision.Value, []byte(`"blockTimestamp"`)) {
		t.Fatalf("value = %s", decision.Value)
	}
}

func TestCanonicalReceiptRemovedCannotDefaultFromNull(t *testing.T) {
	for _, test := range []struct {
		name  string
		raw   json.RawMessage
		valid bool
	}{
		{"missing", nil, false}, {"null", json.RawMessage(`null`), false}, {"false", json.RawMessage(`false`), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, nodes := fleet(t, 3)
			hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
			raw := receiptLogField(t, fixtureReceipt(t, hash, 16), "removed", test.raw)
			for _, node := range nodes {
				node.Return(methods.MethodGetTransactionReceipt, raw)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := client.TransactionReceipt(ctx, hash)
			if test.valid {
				if err != nil || result.Logs[0].Removed {
					t.Fatalf("result = %+v err %v", result, err)
				}
				return
			}
			if !errors.Is(err, rpc.ErrNoValidResponse) {
				t.Fatalf("err = %v", err)
			}
			decision, err := client.Unwrap().Call(ctx, methods.MethodGetTransactionReceipt, hash.Hex())
			if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown || len(decision.Support) != 0 || len(decision.Faults) != 3 {
				t.Fatalf("decision = %+v err %v", decision, err)
			}
		})
	}
}

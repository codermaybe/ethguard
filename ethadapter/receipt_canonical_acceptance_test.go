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
	gethrpc "github.com/ethereum/go-ethereum/rpc"

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
	client, nodes := fleet(t, 2)
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

func TestCanonicalBlockReceiptsThroughAdapter(t *testing.T) {
	client, nodes := fleet(t, 3)
	hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
	raw := receiptLogField(t, fixtureReceipt(t, hash, 16), "blockTimestamp", json.RawMessage(`"0xAB"`))
	for i, node := range nodes {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		obj["from"] = json.RawMessage(`"0x` + strings.Repeat(string(rune('a'+i)), 40) + `"`)
		obj["vendor"] = json.RawMessage(`1`)
		encoded, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{encoded})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].TxHash != hash || result[0].Logs[0].BlockTimestamp != 171 {
		t.Fatalf("result = %+v", result)
	}
	decision, err := client.Unwrap().Call(ctx, methods.MethodGetBlockReceipts, "0x10")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || len(decision.Support) != 3 || bytes.Contains(decision.Value, []byte(`"from"`)) || bytes.Contains(decision.Value, []byte(`"vendor"`)) || !bytes.Contains(decision.Value, []byte(`"blockTimestamp":"0xab"`)) {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestCanonicalBlockReceiptsBadElementKeepsDenominator(t *testing.T) {
	for _, badKind := range []string{"null", "missing-logs", "null-removed"} {
		t.Run(badKind, func(t *testing.T) {
			client, nodes := fleet(t, 4)
			hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
			good := fixtureReceipt(t, hash, 16)
			var other map[string]json.RawMessage
			if err := json.Unmarshal(good, &other); err != nil {
				t.Fatal(err)
			}
			other["gasUsed"] = json.RawMessage(`"0x1"`)
			nodes[0].Return(methods.MethodGetBlockReceipts, []json.RawMessage{good})
			nodes[1].Return(methods.MethodGetBlockReceipts, []json.RawMessage{good})
			nodes[2].Return(methods.MethodGetBlockReceipts, []any{other})
			var bad any
			switch badKind {
			case "null":
				bad = nil
			case "missing-logs":
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(good, &obj); err != nil {
					t.Fatal(err)
				}
				delete(obj, "logs")
				bad = obj
			case "null-removed":
				bad = receiptLogField(t, good, "removed", json.RawMessage(`null`))
			}
			nodes[3].Return(methods.MethodGetBlockReceipts, []any{good, bad})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			decision, err := client.Unwrap().Call(ctx, methods.MethodGetBlockReceipts, "0x10")
			if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown || len(decision.Support) != 2 || len(decision.Reject) != 1 || len(decision.Faults) != 1 {
				t.Fatalf("decision = %+v err %v", decision, err)
			}
			if badKind == "null-removed" && !strings.Contains(decision.Faults[0].Error(), "encode:") {
				t.Fatalf("fault = %v", decision.Faults)
			}
		})
	}
}

func TestCanonicalReceiptsAcceptProtectedStringEscapes(t *testing.T) {
	for _, method := range []string{methods.MethodGetTransactionReceipt, methods.MethodGetBlockReceipts} {
		t.Run(method, func(t *testing.T) {
			client, nodes := fleet(t, 3)
			hash := common.HexToHash("0x" + strings.Repeat("ab", 32))
			raw := fixtureReceipt(t, hash, 16)
			// Escape every protected hex string: hash, quantity, log data, and bloom.
			escaped := json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`))
			for i, node := range nodes {
				body := raw
				if i == 0 {
					body = escaped
				}
				if method == methods.MethodGetBlockReceipts {
					node.Return(method, []json.RawMessage{body})
				} else {
					node.Return(method, body)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if method == methods.MethodGetBlockReceipts {
				result, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
				if err != nil || len(result) != 1 || result[0].TxHash != hash || !bytes.Equal(result[0].Logs[0].Data, []byte{1, 2}) {
					t.Fatalf("result = %+v err %v", result, err)
				}
			} else {
				result, err := client.TransactionReceipt(ctx, hash)
				if err != nil || result.TxHash != hash || !bytes.Equal(result.Logs[0].Data, []byte{1, 2}) {
					t.Fatalf("result = %+v err %v", result, err)
				}
			}
			ref := hash.Hex()
			if method == methods.MethodGetBlockReceipts {
				ref = "0x10"
			}
			decision, err := client.Unwrap().Call(ctx, method, ref)
			if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 3 || bytes.Contains(decision.Value, []byte(`\u0030x`)) {
				t.Fatalf("decision = %+v err %v", decision, err)
			}
		})
	}
}

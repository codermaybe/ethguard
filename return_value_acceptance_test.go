package ethguard_test

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

// These exercise the public client and Aggregator, including the frozen
// invocation denominator and typed reads of canonical Value.
func TestReturnValueAccessListThroughClient(t *testing.T) {
	for _, ref := range []string{"0x10", "pending"} {
		t.Run(ref, func(t *testing.T) {
			client, nodes := fleet(t, 1, 1, 3)
			const address = "0x0000000000000000000000000000000000000abc"
			const key = "0x00000000000000000000000000000000000000000000000000000000000000ab"
			raw := `{"vendor":1,"gasUsed":"0xAB","error":"\u003cdenied\u003e","accessList":[{"storageKeys":["` + strings.ToUpper(key) + `"],"address":"` + strings.ToUpper(address) + `"}]}`
			canonical := `{"accessList":[{"address":"` + address + `","storageKeys":["` + key + `"]}],"error":"<denied>","gasUsed":"0xab"}`
			nodes[0].Return(methods.MethodCreateAccessList, json.RawMessage(raw))
			nodes[1].Return(methods.MethodCreateAccessList, json.RawMessage(canonical))
			nodes[2].Return(methods.MethodCreateAccessList, json.RawMessage(canonical))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, decision, err := client.CreateAccessList(ctx, map[string]any{"to": address}, ref)
			if err != nil {
				t.Fatal(err)
			}
			if string(decision.Value) != canonical || result.GasUsed != 171 || result.Error != "<denied>" || len(result.AccessList) != 1 || len(result.AccessList[0].StorageKeys) != 1 {
				t.Fatalf("result = %+v decision = %+v", result, decision)
			}
			if ref == "pending" {
				if decision.Status != rpc.Selected || decision.Representative != (rpc.SourceRef{}) || len(decision.Support) != 1 {
					t.Fatalf("pending = %+v", decision)
				}
			} else if decision.Status != rpc.Corroborated || decision.Representative != decision.Support[0] {
				t.Fatalf("corroborated = %+v", decision)
			}
		})
	}
}

func TestReturnValueFaultKeepsClientDenominator(t *testing.T) {
	for _, method := range []string{methods.MethodCreateAccessList, methods.MethodGetTransactionReceipt} {
		t.Run(method, func(t *testing.T) {
			client, nodes := fleet(t, 1, 1, 4, ethguard.WithQuorum(2))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var good, other, invalid any
			var params []any
			if method == methods.MethodCreateAccessList {
				good = json.RawMessage(`{"accessList":[],"gasUsed":"0x1"}`)
				other = json.RawMessage(`{"accessList":[],"gasUsed":"0x2"}`)
				invalid = json.RawMessage(`{"accessList":[{"address":"0x0000000000000000000000000000000000000001"}]}`)
				params = []any{map[string]any{}, "0x10"}
			} else {
				hash := "0x" + strings.Repeat("42", 32)
				good = receiptFixture(hash)
				second := receiptFixture(hash)
				second["gasUsed"] = "0x1"
				other = second
				broken := receiptFixture(hash)
				delete(broken, "logs")
				invalid = broken
				params = []any{hash}
			}
			nodes[0].Return(method, good)
			nodes[1].Return(method, good)
			nodes[2].Return(method, other)
			nodes[3].Return(method, invalid)
			decision, err := client.Call(ctx, method, params...)
			if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown || len(decision.Support) != 2 || len(decision.Reject) != 1 || len(decision.Faults) != 1 {
				t.Fatalf("2 matching + 1 different + 1 fault must miss majority of 4: %+v err %v", decision, err)
			}
			// Receipt fails at admission before encoding; accessList at EncodeCheck.
			if method == methods.MethodCreateAccessList && !strings.Contains(decision.Faults[0].Error(), "encode:") {
				t.Fatalf("fault = %v", decision.Faults)
			}
			if method == methods.MethodGetTransactionReceipt && !strings.Contains(decision.Faults[0].Error(), "admission: rpc: malformed response: receipt: missing logs") {
				t.Fatalf("receipt fault lost its admission wording: %v", decision.Faults)
			}
		})
	}
}

func TestReturnValueReceiptFromAndSpellingThroughClient(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	hash := "0x" + strings.Repeat("ab", 32)
	for i, node := range nodes {
		raw := receiptFixture(hash)
		raw["from"] = "0x" + strings.Repeat(string(rune('a'+i)), 40)
		raw["vendor"] = i
		if i == 0 {
			raw["transactionHash"] = strings.ToUpper(hash)
			raw["to"] = strings.ToUpper(raw["to"].(string))
		}
		node.Return(methods.MethodGetTransactionReceipt, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, decision, err := client.TransactionReceipt(ctx, common.HexToHash(hash))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || len(decision.Support) != 3 || result.TxHash.Hex() != hash || bytes.Contains(decision.Value, []byte(`"from"`)) || bytes.Contains(decision.Value, []byte(`"vendor"`)) || !bytes.Contains(decision.Value, []byte(`"logs":[]`)) || bytes.Contains(decision.Value, []byte(`"contractAddress"`)) {
		t.Fatalf("receipt = %+v decision = %+v", result, decision)
	}
}

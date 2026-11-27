package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// The subscription proof and registered method must keep the same full-block
// acceptance and error order. The method's separately encoded delivery value
// also pins the private transaction representation the subscription receives.
func TestContentBlockMatchesRegisteredFullBlockChecks(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	spec, ok := methods.SpecFor(methods.MethodGetBlockByHash)
	if !ok || spec.Admit == nil || spec.Encode == nil {
		t.Fatal("full block contract unavailable")
	}
	for _, name := range []string{"valid", "escaped", "extension", "missing size", "header before shape", "request hash", "transaction root", "signature", "hash-only", "null", "spaced null"} {
		t.Run(name, func(t *testing.T) {
			raw := bytes.Clone(f.block)
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			wantHead := f.head
			switch name {
			case "extension":
				fields["provider"] = map[string]any{"opaque": "kept as source evidence"}
			case "missing size":
				delete(fields, "size")
			case "header before shape":
				fields["hash"] = common.Hash{}.Hex()
				delete(fields, "size")
			case "request hash":
				wantHead = types.CopyHeader(f.head)
				wantHead.Extra = []byte("different selected head")
			case "transaction root":
				fields["transactions"] = []any{}
			case "signature":
				fields["transactions"].([]any)[0].(map[string]any)["v"] = "0x0"
			case "hash-only":
				fields["transactions"] = []any{f.txs[0].Hash().Hex()}
			}
			raw = logsJSON(t, fields)
			switch name {
			case "escaped":
				raw = json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`))
			case "null":
				raw = json.RawMessage(`null`)
			case "spaced null":
				raw = json.RawMessage(` null `)
			}
			req := rpc.Request{Method: methods.MethodGetBlockByHash, Params: []any{wantHead.Hash().Hex(), true}}
			_, wantErr := spec.Admit(t.Context(), req, raw, nil)
			var want types.Transactions
			if wantErr == nil {
				canonical, err := spec.Encode(raw)
				wantErr = err
				if wantErr == nil {
					var body struct {
						Transactions types.Transactions `json:"transactions"`
					}
					if err := json.Unmarshal(canonical, &body); err != nil || body.Transactions == nil {
						wantErr = fmt.Errorf("ethguard: full block has no transaction list: %v", err)
					}
					want = body.Transactions
				}
			}
			got, err := verifyBlockTransactions(t.Context(), wantHead, raw, nil)
			if fmt.Sprint(err) != fmt.Sprint(wantErr) || errors.Is(err, rpc.ErrMalformedResponse) != errors.Is(wantErr, rpc.ErrMalformedResponse) {
				t.Fatalf("subscription error %v differs from method %v", err, wantErr)
			}
			wantSuccess := name == "valid" || name == "escaped" || name == "extension"
			if (err == nil) != wantSuccess {
				t.Fatalf("unexpected acceptance: %v", err)
			}
			if name == "header before shape" && !strings.Contains(err.Error(), "wire hash disagrees") {
				t.Fatalf("shape error hid the earlier header error: %v", err)
			}
			if err != nil {
				if got != nil {
					t.Fatal("failed proof returned transactions")
				}
				return
			}
			if !bytes.Equal(logsJSON(t, got), logsJSON(t, want)) || !bytes.Equal(logsJSON(t, got), logsJSON(t, f.txs)) {
				t.Fatal("subscription changed canonical transaction payloads")
			}
		})
	}
}

func TestContentBlockUsesOnlySuppliedSourceForUncles(t *testing.T) {
	uncle, head := entryHeader(7), entryHeader(9)
	head.UncleHash = types.CalcUncleHash([]*types.Header{uncle})
	raw := entryBlockFixture(t, head, []string{uncle.Hash().Hex()}, `[]`)
	calls := 0
	call := func(_ context.Context, method string, params []any) (json.RawMessage, error) {
		calls++
		if method != "eth_getUncleByBlockHashAndIndex" || len(params) != 2 || params[0] != head.Hash().Hex() || params[1] != "0x0" {
			t.Fatalf("uncle escaped the selected source context: %s %v", method, params)
		}
		return testHeader(uncle), nil
	}
	txs, err := verifyBlockTransactions(t.Context(), head, raw, call)
	if err != nil || txs == nil || len(txs) != 0 || calls != 1 {
		t.Fatalf("historical content proof: txs=%v calls=%d err=%v", txs, calls, err)
	}
	if _, err := verifyBlockTransactions(t.Context(), head, raw, nil); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "uncle loader unavailable") {
		t.Fatalf("uncle proof accepted without the supplied source: %v", err)
	}
}

func TestRawBlockDoesNotClaimFullTrieAdmission(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	head := types.CopyHeader(f.head)
	head.TxHash = common.HexToHash("0x" + strings.Repeat("44", 32))
	var fields map[string]any
	if err := json.Unmarshal(f.block, &fields); err != nil {
		t.Fatal(err)
	}
	fields["hash"], fields["transactionsRoot"] = head.Hash().Hex(), head.TxHash.Hex()
	fields["transactions"].([]any)[0].(map[string]any)["blockHash"] = head.Hash().Hex()
	raw := logsJSON(t, fields)
	if _, err := DecodeBlock(raw); err != nil {
		t.Fatalf("direct raw entry acquired a complete trie proof: %v", err)
	}
	if _, err := verifyBlockTransactions(t.Context(), head, raw, nil); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "transactions root mismatch") {
		t.Fatalf("full content proof skipped transaction commitment: %v", err)
	}
}

func TestRawBlockKeepsHeaderSchemaPayloadErrorOrder(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	for _, stage := range []string{"header", "schema", "payload"} {
		t.Run(stage, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal(f.block, &fields); err != nil {
				t.Fatal(err)
			}
			fields["transactions"].([]any)[0].(map[string]any)["v"] = "0x0"
			want := "decode transaction 0"
			if stage != "payload" {
				delete(fields, "size")
				want = "missing required field size"
			}
			if stage == "header" {
				fields["hash"] = common.Hash{}.Hex()
				want = "decode block header: header: wire hash disagrees"
			}
			value, err := DecodeBlock(logsJSON(t, fields))
			if value != nil || !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), want) {
				t.Fatalf("wrong error stage: value=%v err=%v; want %q", value, err, want)
			}
		})
	}
}

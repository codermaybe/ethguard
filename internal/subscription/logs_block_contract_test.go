package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/rpc"
)

// Content proof pins admission stages, error order, and signed transaction output
// independently of the block admission and encoding implementations.
func TestContentBlockAdmissionContract(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	for _, tc := range []struct {
		name      string
		wantError string
		malformed bool
	}{
		{name: "valid"},
		{name: "escaped"},
		{name: "extension"},
		{"missing size", "rpc: malformed response: block shape: block encode: Block: missing required field size", true},
		{"header before shape", "rpc: malformed response: block header: header: wire hash disagrees with computed header hash", true},
		{"request hash", "rpc: malformed response: block hash does not match requested hash", true},
		{"transaction root", "rpc: malformed response: transactions root mismatch", true},
		{"signature", "rpc: malformed response: transaction 0: rpc: malformed response\ntransaction: invalid payload", true},
		{"hash-only", "rpc: malformed response: transaction 0 is hash-only", true},
		{"null", "ethguard: full block has no transaction list: <nil>", false},
		{"spaced null", "rpc: malformed response: block header: header: expected object", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := bytes.Clone(f.block)
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			wantHead := f.head
			switch tc.name {
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
			switch tc.name {
			case "escaped":
				raw = json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`))
			case "null":
				raw = json.RawMessage(`null`)
			case "spaced null":
				raw = json.RawMessage(` null `)
			}
			got, err := verifyBlockTransactions(t.Context(), wantHead, raw, nil)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("valid content rejected: %v", err)
				}
			} else if err == nil || err.Error() != tc.wantError {
				t.Fatalf("content error = %v, want %q", err, tc.wantError)
			}
			if errors.Is(err, rpc.ErrMalformedResponse) != tc.malformed {
				t.Fatalf("malformed classification = %v, want %v", err, tc.malformed)
			}
			if err != nil {
				if got != nil {
					t.Fatal("failed proof returned transactions")
				}
				return
			}
			if !bytes.Equal(logsJSON(t, got), logsJSON(t, f.txs)) {
				t.Fatal("subscription changed canonical transaction payloads")
			}
		})
	}
}

func TestContentBlockUsesOnlySuppliedSourceForUncles(t *testing.T) {
	uncle := makeLogsBlock(t, nil, 7, 1).head
	f := makeLogsBlock(t, nil, 9, 1)
	head := f.head
	head.UncleHash = types.CalcUncleHash([]*types.Header{uncle})
	var fields map[string]any
	if err := json.Unmarshal(f.block, &fields); err != nil {
		t.Fatal(err)
	}
	fields["hash"], fields["sha3Uncles"], fields["uncles"] = head.Hash().Hex(), head.UncleHash.Hex(), []string{uncle.Hash().Hex()}
	raw := logsJSON(t, fields)
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

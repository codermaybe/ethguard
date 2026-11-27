package ethguard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"

	objectblock "github.com/codermaybe/ethguard/internal/ethobject/block"
	"github.com/codermaybe/ethguard/rpc"
)

func TestRawBlockDoesNotClaimFullTrieAdmission(t *testing.T) {
	head, rawBlock := entryBoundBlock(t)
	head = types.CopyHeader(head)
	head.TxHash = common.HexToHash("0x" + strings.Repeat("44", 32))
	var fields map[string]any
	if err := json.Unmarshal(rawBlock, &fields); err != nil {
		t.Fatal(err)
	}
	fields["hash"], fields["transactionsRoot"] = head.Hash().Hex(), head.TxHash.Hex()
	fields["transactions"].([]any)[0].(map[string]any)["blockHash"] = head.Hash().Hex()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBlock(raw); err != nil {
		t.Fatalf("direct raw entry acquired a complete trie proof: %v", err)
	}
	if _, err := objectblock.AdmitBlock(t.Context(), rpc.Request{Params: []any{head.Hash().Hex(), true}}, raw, true, nil); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "transactions root mismatch") {
		t.Fatalf("full content proof skipped transaction commitment: %v", err)
	}
}

func TestRawBlockKeepsHeaderSchemaPayloadErrorOrder(t *testing.T) {
	_, rawBlock := entryBoundBlock(t)
	for _, stage := range []string{"header", "schema", "payload"} {
		t.Run(stage, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal(rawBlock, &fields); err != nil {
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
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			value, err := DecodeBlock(raw)
			if value != nil || !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), want) {
				t.Fatalf("wrong error stage: value=%v err=%v; want %q", value, err, want)
			}
		})
	}
}

func entryBoundBlock(t *testing.T) (*types.Header, json.RawMessage) {
	t.Helper()
	tx, raw := entrySignedTransaction(t)
	head := entryHeader(1)
	head.TxHash = types.DeriveSha(types.Transactions{tx}, trie.NewStackTrie(nil))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	fields["blockHash"], _ = json.Marshal(head.Hash().Hex())
	embedded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return head, entryBlockFixture(t, head, nil, "["+string(embedded)+"]")
}

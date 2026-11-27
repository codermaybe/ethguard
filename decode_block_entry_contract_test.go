package ethguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"

	"github.com/codermaybe/ethguard/rpc"
)

// entryBlockFixture builds one complete official block JSON from a geth
// header with an independently computed hash — an oracle that never calls
// the production decoder to produce its own expectation.
func entryBlockFixture(t *testing.T, head *types.Header, uncles []string, transactions string) json.RawMessage {
	t.Helper()
	block := types.NewBlockWithHeader(head)
	raw, err := head.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", block.Hash().Hex()))
	obj["size"] = json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("0x%x", block.Size())))
	obj["transactions"] = json.RawMessage(transactions)
	// The official Block schema requires a non-null uncles array.
	obj["uncles"] = json.RawMessage(`[]`)
	if len(uncles) > 0 {
		uncleList, err := json.Marshal(uncles)
		if err != nil {
			t.Fatal(err)
		}
		obj["uncles"] = uncleList
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// entryHeader is the shared base header of the direct-entry fixtures: a
// post-merge style header with empty transaction and uncle roots.
func entryHeader(number int64) *types.Header {
	return &types.Header{
		ParentHash:  common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(number),
		GasLimit:    30_000_000,
		Time:        5,
	}
}

// TestPublicDecodeBlockRejectsWrongDeclaredHash pins S5's headline case: the
// public raw entry re-checks the declared wire hash against the recomputed
// header hash; a structurally valid block with a forged hash member fails.
func TestPublicDecodeBlockRejectsWrongDeclaredHash(t *testing.T) {
	raw := entryBlockFixture(t, entryHeader(1), nil, `[]`)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	var wantHash string
	if err := json.Unmarshal(obj["hash"], &wantHash); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", "0x"+strings.Repeat("11", 32)))
	forged, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if block, err := DecodeBlock(forged); err == nil || block != nil {
		t.Fatalf("forged wire hash accepted: %+v", block)
	}
	// The unmodified fixture still decodes, and the declared hash matches
	// the independently computed one.
	block, err := DecodeBlock(raw)
	if err != nil {
		t.Fatalf("valid canonical block rejected: %v", err)
	}
	if string(block.Hash) != wantHash || string(block.Number) != "0x1" {
		t.Fatalf("decoded block hash/number = %s/%s, want %s/0x1", block.Hash, block.Number, wantHash)
	}
}

// entrySignedTransaction signs one legacy transaction with geth as the
// independent oracle and returns the signed tx plus its complete official
// information object (declared hash and sender from geth, pending-null
// inclusion members).
func entrySignedTransaction(t *testing.T) (*types.Transaction, string) {
	t.Helper()
	key, err := crypto.HexToECDSA("feedbeeffeedbeeffeedbeeffeedbeeffeedbeeffeedbeeffeedbeeffeedbeef")
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTransaction(0, common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), big.NewInt(1), 21000, big.NewInt(1), nil)
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", signed.Hash().Hex()))
	obj["from"] = json.RawMessage(fmt.Sprintf("%q", crypto.PubkeyToAddress(key.PublicKey).Hex()))
	// Embedded block transactions keep their own non-pending contract: the
	// inclusion members are present and non-null (the pending-null spelling
	// belongs to the by-hash query errata only).
	for field, value := range map[string]string{
		"blockHash":        "0x" + strings.Repeat("33", 32),
		"blockNumber":      "0x1",
		"blockTimestamp":   "0x5",
		"transactionIndex": "0x0",
	} {
		obj[field] = json.RawMessage(fmt.Sprintf("%q", value))
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return signed, string(out)
}

// entryFullTransaction spells one complete signed legacy transaction
// information object.
func entryFullTransaction(t *testing.T) string {
	t.Helper()
	_, out := entrySignedTransaction(t)
	return out
}

// TestPublicDecodeBlockTransactionRootConsistency pins the empty/non-empty
// transaction-root checks of the direct entry, plus its rejection of
// hash-only embedded lists.
func TestPublicDecodeBlockTransactionRootConsistency(t *testing.T) {
	head := entryHeader(1)
	raw := entryBlockFixture(t, head, nil, `["0x`+strings.Repeat("aa", 32)+`"]`)
	if _, err := DecodeBlock(raw); err == nil || !strings.Contains(err.Error(), "hash-only transaction list") {
		t.Fatalf("hash-only list = %v", err)
	}
	full := "[" + entryFullTransaction(t) + "]"
	nonEmpty := entryBlockFixture(t, head, nil, full)
	if _, err := DecodeBlock(nonEmpty); err == nil || !strings.Contains(err.Error(), "non-empty transactions for an empty transaction root") {
		t.Fatalf("full body under an empty root = %v", err)
	}
	head.TxHash = common.HexToHash("0x" + strings.Repeat("44", 32))
	emptyBody := entryBlockFixture(t, head, nil, `[]`)
	if _, err := DecodeBlock(emptyBody); err == nil || !strings.Contains(err.Error(), "non-empty transaction root") {
		t.Fatalf("empty body under a non-empty root = %v", err)
	}
}

// TestPublicDecodeBlockUncleConsistency pins the uncle-list checks of the
// direct entry: an uncle-free header cannot carry uncles, a non-empty uncle
// root cannot carry an empty list, and — the historical direct-entry
// contract — non-empty uncles require same-source evidence the raw entry
// does not have.
func TestPublicDecodeBlockUncleConsistency(t *testing.T) {
	head := entryHeader(1)
	raw := entryBlockFixture(t, head, []string{"0x" + strings.Repeat("55", 32)}, `[]`)
	if _, err := DecodeBlock(raw); err == nil || !strings.Contains(err.Error(), "uncle-free header") {
		t.Fatalf("uncles under an uncle-free header = %v", err)
	}
	head.UncleHash = common.HexToHash("0x" + strings.Repeat("66", 32))
	empty := entryBlockFixture(t, head, nil, `[]`)
	if _, err := DecodeBlock(empty); err == nil || !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "non-empty uncle root") {
		t.Fatalf("empty list under a non-empty uncle root = %v", err)
	}
}

// TestPublicDecodeBlockRejectsIllegalTransactionPayloads pins the restored
// per-transaction private payload parse of the direct entry: geth's decoder
// rejects a malformed signature (v="0x0" with nonzero r/s) and a gas value
// beyond its width, checks the generated schema cannot express, while a
// valid signed payload embedded under its own trie root still decodes.
func TestPublicDecodeBlockRejectsIllegalTransactionPayloads(t *testing.T) {
	signed, full := entrySignedTransaction(t)
	payloadRoot := types.DeriveSha(types.Transactions{signed}, trie.NewStackTrie(nil))

	// Malformed signature: v below the homestead range with nonzero r/s.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(full), &obj); err != nil {
		t.Fatal(err)
	}
	obj["v"] = json.RawMessage(`"0x0"`)
	badV, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	// Gas beyond the uint64 width of the legacy field.
	var gasObj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(full), &gasObj); err != nil {
		t.Fatal(err)
	}
	gasObj["gas"] = json.RawMessage(`"0x10000000000000000"`)
	badGas, err := json.Marshal(gasObj)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"malformed signature", string(badV)},
		{"gas beyond uint64", string(badGas)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A non-empty declared root lets the answer past the root/list
			// consistency check without claiming the trie was proven.
			head := entryHeader(1)
			head.TxHash = common.HexToHash("0x" + strings.Repeat("44", 32))
			raw := entryBlockFixture(t, head, nil, "["+tc.payload+"]")
			block, err := DecodeBlock(raw)
			if err == nil || block != nil {
				t.Fatalf("illegal embedded payload accepted: %+v", block)
			}
			if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "decode transaction 0") {
				t.Fatalf("error = %v, want malformed response naming transaction 0", err)
			}
		})
	}

	// Positive control: the valid signed payload under its own trie root.
	// Embedded block transactions keep their own non-pending contract: the
	// inclusion members are present and non-null.
	head := entryHeader(1)
	head.TxHash = payloadRoot
	blockHash := types.NewBlockWithHeader(head).Hash()
	var fullObj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(full), &fullObj); err != nil {
		t.Fatal(err)
	}
	fullObj["blockHash"] = json.RawMessage(fmt.Sprintf("%q", blockHash.Hex()))
	fullObj["blockNumber"] = json.RawMessage(`"0x1"`)
	fullObj["blockTimestamp"] = json.RawMessage(`"0x5"`)
	fullObj["transactionIndex"] = json.RawMessage(`"0x0"`)
	embedded, err := json.Marshal(fullObj)
	if err != nil {
		t.Fatal(err)
	}
	raw := entryBlockFixture(t, head, nil, "["+string(embedded)+"]")
	block, err := DecodeBlock(raw)
	if err != nil {
		t.Fatalf("valid embedded payload rejected: %v", err)
	}
	if block.Transactions.FullTransactions == nil || len(*block.Transactions.FullTransactions) != 1 {
		t.Fatalf("embedded transactions = %+v", block.Transactions)
	}
}

// TestPublicDecodeBlockAcceptsGeneratedEquivalentEscapes pins the frozen
// order of the direct entry: the generated model validates and normalizes
// first, so embedded transactions spelled with JSON unicode escapes for the
// "x" of the 0x prefix — a spelling the private geth wire grammar rejects on
// raw bytes — decode through the canonical copy with the transaction hash
// unchanged.
func TestPublicDecodeBlockAcceptsGeneratedEquivalentEscapes(t *testing.T) {
	signed, full := entrySignedTransaction(t)
	payloadRoot := types.DeriveSha(types.Transactions{signed}, trie.NewStackTrie(nil))
	head := entryHeader(1)
	head.TxHash = payloadRoot
	blockHash := types.NewBlockWithHeader(head).Hash()

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(full), &obj); err != nil {
		t.Fatal(err)
	}
	obj["blockHash"] = json.RawMessage(fmt.Sprintf("%q", blockHash.Hex()))
	obj["gas"] = json.RawMessage(`"0\u00785208"`)
	obj["input"] = json.RawMessage(`"0\u0078"`)
	embedded, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}

	raw := entryBlockFixture(t, head, nil, "["+string(embedded)+"]")
	block, err := DecodeBlock(raw)
	if err != nil {
		t.Fatalf("generated-equivalent escape spelling rejected: %v", err)
	}
	if block.Transactions.FullTransactions == nil || len(*block.Transactions.FullTransactions) != 1 {
		t.Fatalf("embedded transactions = %+v", block.Transactions)
	}
	legacy := (*block.Transactions.FullTransactions)[0].SignedLegacyTransaction
	if legacy == nil {
		t.Fatal("embedded transaction projected onto the wrong branch")
	}
	if string(legacy.Hash) != signed.Hash().Hex() {
		t.Fatalf("transaction hash changed through canonicalization: %s != %s", legacy.Hash, signed.Hash().Hex())
	}
	if string(legacy.Gas) != "0x5208" {
		t.Fatalf("canonical gas = %s, want 0x5208", legacy.Gas)
	}
	if string(legacy.Input) != "0x" {
		t.Fatalf("canonical input = %s, want 0x", legacy.Input)
	}
}

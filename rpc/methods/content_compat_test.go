package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func TestRealShapesAgreeAfterPruneAndCaseFold(t *testing.T) {
	blocks := sampleResults(t, "block-receipt-tx", "block-filled-number")
	left, err := normalizeBlockIdentity(blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	right, err := normalizeBlockIdentity(blocks[1])
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("block identity diverged: %s vs %s", left, right)
	}
	withVendor := injectField(t, blocks[0], "vendorExtension", `"nope"`)
	folded := foldHex(t, withVendor)
	got, err := normalizeBlockIdentity(folded)
	if err != nil {
		t.Fatal(err)
	}
	if got != left {
		t.Fatalf("vendor field or hex case changed block identity: %s vs %s", got, left)
	}

	receipts := sampleResults(t, "block-receipt-tx", "receipt-one")
	receiptKey := mustNormalize(t, normalizeReceiptObject, receipts[0])
	if mustNormalize(t, normalizeReceiptObject, receipts[1]) != receiptKey {
		t.Fatal("receipt sources diverged after pruning")
	}
	if mustNormalize(t, normalizeReceiptObject, foldHex(t, injectField(t, receipts[0], "vendorExtension", "1"))) != receiptKey {
		t.Fatal("vendor field or hex case changed the receipt key")
	}

	txs := sampleResults(t, "block-receipt-tx", "tx-one")
	txKey := mustNormalize(t, normalizeTransactionObject, txs[0])
	if mustNormalize(t, normalizeTransactionObject, txs[1]) != txKey {
		t.Fatal("transaction sources diverged after pruning")
	}
	if !bytes.Contains(txs[0], []byte(`"blockTimestamp"`)) || !bytes.Contains(txs[1], []byte(`"blockTimestamp"`)) {
		t.Fatal("capture lost blockTimestamp; both sources carried it")
	}
	if mustNormalize(t, normalizeTransactionObject, foldHex(t, injectField(t, txs[0], "vendorExtension", `"extra"`))) != txKey {
		t.Fatal("vendor field or hex case changed the transaction key")
	}
}

func TestMissingRequiredFieldsStayOutOfAdmission(t *testing.T) {
	receipt := deleteField(t, sampleResults(t, "block-receipt-tx", "receipt-one")[0], "logs")
	req := rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{txHash(t, sampleResults(t, "block-receipt-tx", "receipt-one")[0])}}
	_, err := admitReceipt(context.Background(), req, receipt, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("err = %v, want ErrMalformedResponse", err)
	}
	if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrNoValidResponse) || errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("missing field swallowed as %v", err)
	}
	// Normalize still has a canonical pruned form. Admission, not the key, rejects it.
	if _, err := normalizeReceiptObject(receipt); err != nil {
		t.Fatalf("normalize rejected a shaped receipt; that is admission's job: %v", err)
	}

	tx := deleteField(t, sampleResults(t, "block-receipt-tx", "tx-one")[0], "from")
	txReq := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{txHash(t, sampleResults(t, "block-receipt-tx", "tx-one")[0])}}
	_, err = admitTransaction(context.Background(), txReq, tx, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("tx err = %v, want ErrMalformedResponse", err)
	}
}

func TestAdmitDoesNotChangeComparisonKey(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "receipt-one")[0]
	before := mustNormalize(t, normalizeReceiptObject, raw)
	req := rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{txHash(t, raw)}}
	if _, err := admitReceipt(context.Background(), req, raw, nil); err != nil {
		t.Fatal(err)
	}
	if mustNormalize(t, normalizeReceiptObject, raw) != before {
		t.Fatal("admission changed the receipt comparison key")
	}
}

func TestNullAnswersStayDistinct(t *testing.T) {
	if got, err := normalizeBlockIdentity(json.RawMessage("null")); err != nil || got != "null" {
		t.Fatalf("block null = %q, %v", got, err)
	}
	if _, err := normalizeLogs(json.RawMessage("null")); err == nil {
		t.Fatal("log null must not be a comparable answer")
	}
	if got := mustNormalize(t, normalizeReceiptObject, json.RawMessage("null")); got != "null" {
		t.Fatalf("receipt null = %q", got)
	}
	empty := mustNormalize(t, normalizeBlockReceiptList, sampleResults(t, "block-receipt-tx", "receipts-empty")[0])
	if empty == "null" {
		t.Fatal("empty receipt array collapsed into null")
	}
	if got := mustNormalize(t, normalizeTransactionObject, json.RawMessage("null")); got != "null" {
		t.Fatalf("transaction null = %q", got)
	}
	if got := mustNormalize(t, normalizeTransactionObject, sampleResults(t, "block-receipt-tx", "tx-one")[0]); got == "null" {
		t.Fatal("transaction object collapsed into null")
	}
}

func TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected(t *testing.T) {
	empty := sampleResults(t, "block-receipt-tx", "block-empty-hash")[0]
	filled := sampleResults(t, "block-receipt-tx", "block-filled-hash")[0]
	var head struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(empty, &head); err != nil {
		t.Fatal(err)
	}
	policy := BlockByHashPolicy().(rpc.AdmissionPolicy)
	if _, err := policy.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByHash, Params: []any{head.Hash, true}}, empty, nil); err != nil {
		t.Fatalf("empty uncle list and empty transaction list must be legal: %v", err)
	}
	var other struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(filled, &other); err != nil {
		t.Fatal(err)
	}
	_, err := policy.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByHash, Params: []any{other.Hash, true}}, empty, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("foreign block content err = %v, want ErrMalformedResponse", err)
	}
	if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrNoValidResponse) || errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("impersonation swallowed as %v", err)
	}
	tampered := injectField(t, empty, "uncles", `["0x`+strings.Repeat("ab", 32)+`"]`)
	_, err = policy.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByHash, Params: []any{head.Hash, true}}, tampered, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("non-empty uncles for an empty root: %v", err)
	}
}

func TestReceiptFromAnotherTransactionIsRejected(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "receipt-one")[0]
	// The captured tx and its receipt share a hash (txHash parses it, which
	// also verifies the fixture); substitute a different hash.
	_ = txHash(t, sampleResults(t, "block-receipt-tx", "tx-one")[0])
	other := "0x" + strings.Repeat("12", 32)
	_, err := admitReceipt(context.Background(), rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{other}}, raw, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("err = %v, want ErrMalformedResponse", err)
	}
	if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrNoValidResponse) || errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("receipt impersonation swallowed as %v", err)
	}
	rows := json.RawMessage("[" + string(raw) + "]")
	var block struct {
		Hash   string `json:"hash"`
		Number string `json:"number"`
	}
	if err := json.Unmarshal(sampleResults(t, "block-receipt-tx", "block-filled-number")[0], &block); err != nil {
		t.Fatal(err)
	}
	_, err = admitBlockReceipts(context.Background(), rpc.Request{Method: MethodGetBlockReceipts, Params: []any{block.Hash}}, json.RawMessage("[]"), nil)
	if err != nil {
		t.Fatalf("empty receipt array for a real block hash must stay legal: %v", err)
	}
	_, err = admitBlockReceipts(context.Background(), rpc.Request{Method: MethodGetBlockReceipts, Params: []any{"0x" + strings.Repeat("34", 32)}}, rows, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("receipts of another block: %v", err)
	}
}

func TestWireNormalizeDoesNotRejectUnknownFields(t *testing.T) {
	raw := injectField(t, sampleResults(t, "block-receipt-tx", "receipt-one")[0], "vendorExtension", `{"nested":true}`)
	key, err := wire.PruneMethod(MethodGetTransactionReceipt, raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(key, "vendorExtension") {
		t.Fatal("prune kept an unknown field")
	}
}

func sampleResults(t *testing.T, dir, requestID string) []json.RawMessage {
	t.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("..", "..", "internal", "rpcsample", "testdata", dir, "2026-09-23"))
	if err != nil {
		t.Fatal(err)
	}
	var out []json.RawMessage
	for _, record := range records {
		if record.RequestID != requestID {
			continue
		}
		_, body, err := replay.BodyOf(record)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		out = append(out, doc.Result)
	}
	if len(out) < 2 {
		t.Fatalf("%s/%s: got %d results", dir, requestID, len(out))
	}
	return out
}

func mustNormalize(t *testing.T, fn func(json.RawMessage) (string, error), raw json.RawMessage) string {
	t.Helper()
	got, err := fn(raw)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func injectField(t *testing.T, raw json.RawMessage, key, value string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj[key] = json.RawMessage(value)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func deleteField(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, key)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func foldHex(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	upperHex(&value)
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func upperHex(value *any) {
	switch item := (*value).(type) {
	case map[string]any:
		for key, child := range item {
			upperHex(&child)
			item[key] = child
		}
	case []any:
		for i := range item {
			upperHex(&item[i])
		}
	case string:
		if strings.HasPrefix(item, "0x") {
			*value = "0x" + strings.ToUpper(item[2:])
		}
	}
}

func txHash(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var doc struct {
		Hash            string `json:"hash"`
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TransactionHash != "" {
		return doc.TransactionHash
	}
	return doc.Hash
}

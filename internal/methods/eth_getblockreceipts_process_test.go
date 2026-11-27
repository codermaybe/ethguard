package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	receiptobj "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/rpc"
)

func blockReceiptProcessingRequest(t *testing.T, raw json.RawMessage) rpc.Request {
	t.Helper()
	var fields struct {
		BlockHash string `json:"blockHash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.BlockHash == "" {
		t.Fatalf("fixture block hash: %v", err)
	}
	return rpc.Request{Method: MethodGetBlockReceipts, Params: []any{fields.BlockHash}}
}

func TestBlockReceiptsProcessingEntryRegistered(t *testing.T) {
	spec := getBlockReceiptsSpec()
	plan, err := CompilePlan(spec)
	if err != nil || spec.ProcessCanonical == nil || plan.Canonical == nil || plan.Resolver != nil || plan.Admit != nil || spec.KeySource != rpcengine.RuleKeyCanonicalBytes {
		t.Fatalf("incomplete processing registration: %+v, %v", spec, err)
	}
}

// The registered processor commits the object encoder output and no evidence.
func TestBlockReceiptsProcessingDelivery(t *testing.T) {
	receipt := sampleReceipt(t)
	req := blockReceiptProcessingRequest(t, receipt)
	spec := getBlockReceiptsSpec()
	for _, raw := range []json.RawMessage{
		json.RawMessage(`null`), json.RawMessage(`[]`), receiptArray(t, receipt),
		receiptArray(t, reorderReceipt(t, receipt), foldReceiptHex(t, receipt)),
		receiptArray(t, injectReceiptField(t, receipt, "vendor", `{"fee":"0x1"}`)),
	} {
		want, err := receiptobj.EncodeBlock(raw)
		if err != nil {
			t.Fatal(err)
		}

		if spec.ProcessCanonical != nil {
			result, err := spec.ProcessCanonical(context.Background(), req, raw, nil)
			if err != nil || !bytes.Equal(result.Value(), want) || result.Evidence() != nil {
				t.Fatalf("processed = %s, evidence=%s: %v; want %s", result.Value(), result.Evidence(), err, want)
			}
		}
	}
}

func TestBlockReceiptsProcessingFailures(t *testing.T) {
	receipt := sampleReceipt(t)
	req := blockReceiptProcessingRequest(t, receipt)
	spec := getBlockReceiptsSpec()
	for _, raw := range []json.RawMessage{
		json.RawMessage(`[null]`), json.RawMessage(`{}`),
		receiptArray(t, receipt, deleteReceiptField(t, receipt, "logs")),
	} {

		if spec.ProcessCanonical != nil {
			result, err := spec.ProcessCanonical(context.Background(), req, raw, nil)
			if !errors.Is(err, rpc.ErrMalformedResponse) || result.Value() != nil || result.Evidence() != nil {
				t.Fatalf("processed failure = %+v, %v", result, err)
			}
		}
	}
}

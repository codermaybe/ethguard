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
	if err != nil || spec.ProcessCanonical == nil || plan.ProcessCanonical == nil || plan.Admit == nil || spec.KeySource != rpcengine.RuleKeyCanonicalBytes {
		t.Fatalf("incomplete processing registration: %+v, %v", spec, err)
	}
}

// The raw entry remains executable in every revision, including the frozen
// pre-processor baseline. A registered processor must produce the same bytes
// and absent evidence; no comparison normalizer is used as the expected value.
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
		evidence, err := spec.Admit(context.Background(), req, raw, nil)
		if err != nil || evidence != nil {
			t.Fatalf("raw admission = %s, %v", evidence, err)
		}
		got, err := spec.Encode(raw)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("raw bytes = %s, want %s: %v", got, want, err)
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
		evidence, want := spec.Admit(context.Background(), req, raw, nil)
		if evidence != nil || !errors.Is(want, rpc.ErrMalformedResponse) {
			t.Fatalf("raw error = %v, evidence=%s", want, evidence)
		}
		if spec.ProcessCanonical != nil {
			result, err := spec.ProcessCanonical(context.Background(), req, raw, nil)
			if !errors.Is(err, rpc.ErrMalformedResponse) || err.Error() != want.Error() || result.Value() != nil || result.Evidence() != nil {
				t.Fatalf("processed failure = %+v, %v; want %v", result, err, want)
			}
		}
	}
}

package methods

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	receiptobj "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/rpc"
)

// The receipt processing entry's contract, with expectations written out
// independently: literal error texts, the encoder's own output, and explicit
// sentinel classes. Neither the object preparation nor the compatibility
// admission supplies the expected values, so a drift in the fixed order, the
// wraps, or the result/evidence pairing fails here.

func TestReceiptProcessingEntryIsRegistered(t *testing.T) {
	spec, ok := SpecFor(MethodGetTransactionReceipt)
	if !ok {
		t.Fatal("receipt spec missing")
	}
	if spec.ProcessCanonical == nil {
		t.Fatal("receipt spec declares no processor")
	}
	if spec.KeySource != rpcengine.RuleKeyCanonicalBytes || spec.Admit != nil || spec.Encode != nil {
		t.Fatalf("processing assembly prerequisites changed: %+v", spec)
	}
	plan, err := CompilePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Canonical == nil || plan.Admit != nil || plan.Resolver != nil {
		t.Fatalf("compiled plan lost its route: process=%t admit=%t", plan.Canonical != nil, plan.Admit != nil)
	}
}

func TestReceiptProcessingEntrySuccessCarriesNilEvidence(t *testing.T) {
	raw := sampleReceipt(t)
	var fields struct {
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.TransactionHash == "" {
		t.Fatalf("sample carries no transactionHash: %v", err)
	}
	req := rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{fields.TransactionHash}}
	result, err := processReceipt(context.Background(), req, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Independent expectation: the canonical bytes are the encoder's own
	// output for the same input, the value the raw route commits today.
	want, err := receiptobj.Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(result.Value(), want) {
		t.Fatalf("value = %s, want the encoder's bytes %s", result.Value(), want)
	}
	if result.Evidence() != nil {
		t.Fatalf("success carried evidence: %s", result.Evidence())
	}
	// A second spelling of the same source processes to the identical bytes.
	reordered := reorderReceipt(t, raw)
	again, err := processReceipt(context.Background(), req, reordered, nil)
	if err != nil || !jsonEqual(again.Value(), want) {
		t.Fatalf("reordered spelling = %s, %v", again.Value(), err)
	}
}

func TestReceiptProcessingEntryNullIsCanonicalNull(t *testing.T) {
	req := rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{"0x" + strings.Repeat("11", 32)}}
	result, err := processReceipt(context.Background(), req, json.RawMessage(`null`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Value()) != "null" || result.Evidence() != nil {
		t.Fatalf("null = %s, evidence %v", result.Value(), result.Evidence())
	}
}

func TestReceiptProcessingEntryFailuresReturnZeroResultAndUnchangedErrors(t *testing.T) {
	raw := sampleReceipt(t)
	otherHash := "0x" + strings.Repeat("22", 32)
	self := sampleReceiptHash(t)
	cases := []struct {
		name  string
		req   rpc.Request
		raw   json.RawMessage
		class error
		want  string
	}{
		{
			name:  "bad-request-grammar",
			req:   rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{123}},
			raw:   raw,
			class: rpc.ErrInvalidRequest,
			want:  "rpc: invalid request: transaction hash must be a 32-byte hex string",
		},
		{
			name:  "missing-logs",
			req:   rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{self}},
			raw:   deleteReceiptField(t, raw, "logs"),
			class: rpc.ErrMalformedResponse,
			want:  "rpc: malformed response: receipt: logs must be an array",
		},
		{
			name:  "non-object",
			req:   rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{self}},
			raw:   json.RawMessage(`"0x1"`),
			class: rpc.ErrMalformedResponse,
			want:  "rpc: malformed response: receipt: expected an object",
		},
		{
			name:  "bloom-mismatch",
			req:   rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{self}},
			raw:   replaceReceiptField(t, raw, "logsBloom", `"0x`+strings.Repeat("00", 255)+`01"`),
			class: rpc.ErrMalformedResponse,
			want:  "rpc: malformed response: receipt: logsBloom disagrees with logs",
		},
		{
			name:  "binding-mismatch",
			req:   rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{otherHash}},
			raw:   raw,
			class: rpc.ErrMalformedResponse,
			want:  "rpc: malformed response: receipt transaction hash does not match requested hash",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result, err := processReceipt(context.Background(), item.req, item.raw, nil)
			if result.Value() != nil || result.Evidence() != nil {
				t.Fatalf("failure returned a result: value=%s evidence=%s", result.Value(), result.Evidence())
			}
			if err == nil || !errors.Is(err, item.class) || err.Error() != item.want {
				t.Fatalf("err = %v, want %q (class %v)", err, item.want, item.class)
			}
		})
	}
	t.Run("shape-before-binding", func(t *testing.T) {
		req := rpc.Request{Method: MethodGetTransactionReceipt, Params: []any{otherHash}}
		result, err := processReceipt(context.Background(), req, deleteReceiptField(t, raw, "logs"), nil)
		if result.Value() != nil || err == nil || !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), "does not match requested hash") {
			t.Fatalf("binding mismatch preempted the shape error: %v", err)
		}
	})
}

// sampleReceiptHash extracts the sample's own transactionHash once.
func sampleReceiptHash(t *testing.T) string {
	t.Helper()
	raw := sampleReceipt(t)
	var fields struct {
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.TransactionHash == "" {
		t.Fatalf("sample carries no transactionHash: %v", err)
	}
	return fields.TransactionHash
}

// jsonEqual compares two JSON values by their decoded content, so member
// order in a diagnostic dump cannot fail an assertion about content.
func jsonEqual(left, right json.RawMessage) bool {
	var l, r any
	if err := json.Unmarshal(left, &l); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &r); err != nil {
		return false
	}
	lb, _ := json.Marshal(l)
	rb, _ := json.Marshal(r)
	return string(lb) == string(rb)
}

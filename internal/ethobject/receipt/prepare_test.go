package receipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The PrepareTransaction contract: the admission order, error classes, and
// texts are exactly AdmitTransaction's, with the validated canonical bytes
// as the additional success output. Expectations below are written out
// independently (literal texts, the encoder's own output, explicit sentinel
// classes), so a drift in the fixed order or in any wrap is a failure here.

func TestPrepareTransactionNullShortCircuitBeforeRequest(t *testing.T) {
	// The null short-circuit runs before the request is parsed: a JSON null
	// is absent-or-null semantics, never a request error, whatever the
	// request carries.
	canonical, err := PrepareTransaction(rpc.Request{Params: []any{123}}, json.RawMessage(`null`))
	if err != nil || string(canonical) != "null" {
		t.Fatalf("null = %s, %v", canonical, err)
	}
	canonical, err = PrepareTransaction(rpc.Request{}, json.RawMessage(`  null  `))
	if err != nil || string(canonical) != "null" {
		t.Fatalf("padded null = %s, %v", canonical, err)
	}
}

func TestPrepareTransactionRequestErrorPriority(t *testing.T) {
	// A request error keeps its class and wins over any payload verdict:
	// the payload is never decoded for a request that cannot name a hash.
	for _, params := range [][]any{
		{123},
		{"0xzz"},
		{[]any{"0xaa"}},
		nil,
	} {
		canonical, err := PrepareTransaction(rpc.Request{Params: params}, receiptFixture(t))
		if canonical != nil || err == nil || !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("params %v = %s, %v; want ErrInvalidRequest and a zero result", params, canonical, err)
		}
		if AdmitTransaction(rpc.Request{Params: params}, receiptFixture(t)) == nil {
			t.Fatal("admit accepted a request the preparation rejected")
		}
	}
}

func TestPrepareTransactionReturnsValidatedCanonicalBytes(t *testing.T) {
	raw := receiptFixture(t, logFixture())
	canonical, err := PrepareTransaction(rpc.Request{Params: []any{receiptTxHash}}, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Independent expectation: the encoder's own output for the same input,
	// which the raw route commits today.
	want, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, want) {
		t.Fatalf("canonical bytes diverge from the encoder:\n%s\n%s", canonical, want)
	}
	// The bound request is the receipt's own transactionHash; a second
	// spelling of the same source folds to the identical bytes.
	var fields struct {
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.TransactionHash == "" {
		t.Fatalf("fixture carries no transactionHash: %v", err)
	}
	shouted := strings.ToUpper(receiptTxHash)
	again, err := PrepareTransaction(rpc.Request{Params: []any{shouted}}, replaceField(t, raw, "transactionHash", shouted))
	if err != nil || !bytes.Equal(again, canonical) {
		t.Fatalf("case-folded spelling = %s, %v", again, err)
	}
}

func TestPrepareTransactionMalformedWrapUnchanged(t *testing.T) {
	base := receiptFixture(t, logFixture())
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"missing-logs", deleteField(t, base, "logs"), "receipt: logs must be an array"},
		{"non-object", json.RawMessage(`"0x1"`), "receipt: expected an object"},
		{
			"bloom-mismatch",
			replaceField(t, base, "logsBloom", "0x"+strings.Repeat("00", 255)+"01"),
			"receipt: logsBloom disagrees with logs",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			canonical, err := PrepareTransaction(rpc.Request{Params: []any{receiptTxHash}}, item.raw)
			if canonical != nil {
				t.Fatalf("failure returned canonical bytes: %s", canonical)
			}
			if err == nil || !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("err = %v, want the malformed class", err)
			}
			if got := err.Error(); !strings.HasSuffix(got, ": "+item.want) {
				t.Fatalf("err = %q, want the unchanged text with %q", got, item.want)
			}
			// The admission entry reports the identical error.
			admitErr := AdmitTransaction(rpc.Request{Params: []any{receiptTxHash}}, item.raw)
			if admitErr == nil || admitErr.Error() != err.Error() {
				t.Fatalf("admit error = %v, preparation error = %v", admitErr, err)
			}
		})
	}
}

// The raw route's priority: a payload shape error classifies before a
// binding mismatch, because the payload is validated before the requested
// hash is compared.
func TestPrepareTransactionShapeBeforeBinding(t *testing.T) {
	otherHash := "0x" + strings.Repeat("22", 32)
	shaped := deleteField(t, receiptFixture(t, logFixture()), "logs")
	canonical, err := PrepareTransaction(rpc.Request{Params: []any{otherHash}}, shaped)
	if canonical != nil || err == nil || !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("err = %v, want the shape error to win with the malformed class", err)
	}
	if strings.Contains(err.Error(), "does not match requested hash") {
		t.Fatalf("binding mismatch preempted the shape error: %v", err)
	}
}

// A valid receipt bound to a different transaction is a binding mismatch,
// not a shape error.
func TestPrepareTransactionBindingMismatch(t *testing.T) {
	otherHash := "0x" + strings.Repeat("22", 32)
	canonical, err := PrepareTransaction(rpc.Request{Params: []any{otherHash}}, receiptFixture(t, logFixture()))
	if canonical != nil || err == nil || !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("err = %v, want the malformed class", err)
	}
	if !strings.Contains(err.Error(), "receipt transaction hash does not match requested hash") {
		t.Fatalf("err = %v, want the binding text", err)
	}
	if AdmitTransaction(rpc.Request{Params: []any{otherHash}}, receiptFixture(t, logFixture())) == nil {
		t.Fatal("admit accepted a foreign receipt")
	}
}

func deleteField(t *testing.T, raw json.RawMessage, name string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, name)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

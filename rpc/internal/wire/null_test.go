package wire_test

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func TestNullableRootRequiresCompleteJSONNull(t *testing.T) {
	value, err := wire.NewGetTransactionReceiptResult(json.RawMessage(" \t\r\nnull \t\r\n"))
	if err != nil || value != nil {
		t.Fatalf("JSON whitespace must preserve null: %v, %v", value, err)
	}
	for _, raw := range []string{"\vnull", "null\u00a0", "null false"} {
		if _, err := wire.NewGetTransactionReceiptResult(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed null %q must fail", raw)
		}
	}
}

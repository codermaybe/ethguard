package ethapi_test

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestNullableRootRequiresCompleteJSONNull(t *testing.T) {
	value, err := ethapi.NewGetTransactionReceiptResult(json.RawMessage(" \t\r\nnull \t\r\n"))
	if err != nil || value != nil {
		t.Fatalf("JSON whitespace must preserve null: %v, %v", value, err)
	}
	for _, raw := range []string{"\vnull", "null\u00a0", "null false"} {
		if _, err := ethapi.NewGetTransactionReceiptResult(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed null %q must fail", raw)
		}
	}
}

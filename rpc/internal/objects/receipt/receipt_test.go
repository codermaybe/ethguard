package receipt

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReceiptSemanticJSONPreservesNumberTokensAndSource(t *testing.T) {
	raw := json.RawMessage(`{"hash":"\u0030xAB","vendorInteger":9007199254740993,"vendorDecimal":1.0000000000000000000001,"vendorExponent":1e1000,"optional":null}`)
	original := append(json.RawMessage(nil), raw...)
	plain, err := receiptSemanticJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"hash":"0xAB"`, `9007199254740993`, `1.0000000000000000000001`, `1e1000`, `"optional":null`} {
		if !bytes.Contains(plain, []byte(want)) {
			t.Fatalf("lost token %s: %s", want, plain)
		}
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("source payload changed")
	}
}

package receipt

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestSharedReceiptRejectsOriginalDuplicateTransactionHash(t *testing.T) {
	hash := `"0x` + strings.Repeat("11", 32) + `"`
	raw := json.RawMessage(`{"type":"0x2","status":"0x1","cumulativeGasUsed":"0x1","gasUsed":"0x1","effectiveGasPrice":"0x1","logsBloom":"0x` + strings.Repeat("00", 256) + `","logs":[],"transactionHash":` + hash + `,"transactionIndex":"0x0","blockHash":"0x` + strings.Repeat("22", 32) + `","blockNumber":"0x1","from":"0x` + strings.Repeat("00", 20) + `"}`)
	if _, err := Encode(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(raw); err != nil {
		t.Fatal(err)
	}
	// Equal duplicate values must also fail before a map erases their presence.
	duplicate := append([]byte(`{"transactionHash":`+hash+`,`), raw[1:]...)
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"encode", func() error { _, err := Encode(duplicate); return err }},
		{"decode", func() error { _, err := Decode(duplicate); return err }},
		{"list", func() error { _, err := EncodeBlock(append(append([]byte{'['}, duplicate...), ']')); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, jsonsafe.ErrDuplicateKey) {
				t.Fatalf("duplicate receipt declaration was erased before validation: %v", err)
			}
		})
	}
}

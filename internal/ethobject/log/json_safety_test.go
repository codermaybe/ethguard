package log

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestSharedLogRejectsOriginalDuplicateDeclarations(t *testing.T) {
	raw := json.RawMessage(`{"address":"0x` + strings.Repeat("00", 20) + `","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x` + strings.Repeat("11", 32) + `","transactionIndex":"0x0","blockHash":"0x` + strings.Repeat("22", 32) + `","logIndex":"0x0","removed":false}`)
	if _, err := EncodeOne(raw); err != nil {
		t.Fatal(err)
	}
	// Equal duplicate values must also fail before a map erases their presence.
	duplicate := append([]byte(`{"removed":false,`), raw[1:]...)
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"validate", func() error { return Validate(duplicate) }},
		{"object", func() error { _, err := EncodeOne(duplicate); return err }},
		{"list", func() error { _, err := Encode(append(append([]byte{'['}, duplicate...), ']')); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, jsonsafe.ErrDuplicateKey) {
				t.Fatalf("duplicate log declaration was erased before validation: %v", err)
			}
		})
	}
}

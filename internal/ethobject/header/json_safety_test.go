package header

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestSharedHeaderRejectsOriginalDuplicateHash(t *testing.T) {
	raw := testHeader(t)
	if _, err := Normalize(raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	// The repeated declaration is byte-for-byte equal. Map decoding would
	// erase even this ambiguity before the commitment checker could see it.
	duplicate := append(append([]byte(`{"hash":`), append(fields["hash"], ',')...), raw[1:]...)
	for _, tc := range []struct {
		name string
		run  func(json.RawMessage) error
	}{
		{"projection", func(raw json.RawMessage) error { _, err := Projection(raw); return err }},
		{"decode", func(raw json.RawMessage) error { _, err := Decode(raw); return err }},
		{"normalize", func(raw json.RawMessage) error { _, err := Normalize(raw); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(duplicate); !errors.Is(err, jsonsafe.ErrDuplicateKey) {
				t.Fatalf("duplicate hash was erased before validation: %v", err)
			}
		})
	}
}

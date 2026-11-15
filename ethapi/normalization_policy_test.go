package ethapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

// Expected values are independent literals, not another call to the shared
// normalizer. This separates compatibility input from raw schema conformance.
func TestGeneratedFixedHexCompatibilityHasCanonicalOfficialOutput(t *testing.T) {
	for _, tc := range []struct {
		name, raw, expected, pattern string
		parse                        func(json.RawMessage) (string, error)
	}{
		{"hash", `"0X` + strings.Repeat("A", 64) + `"`, `"0x` + strings.Repeat("a", 64) + `"`, `^0x[0-9a-f]{64}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHash(raw)
			return string(value), err
		}},
		{"address", `"0X` + strings.Repeat("A", 40) + `"`, `"0x` + strings.Repeat("a", 40) + `"`, `^0x[0-9a-fA-F]{40}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewAddress(raw)
			return string(value), err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := map[string]any{"type": "string", "pattern": tc.pattern}
			if err := conformance.ValidateResult(schema, json.RawMessage(tc.raw)); err == nil {
				t.Fatal("raw source schema accepted the compatibility spelling")
			}
			value, err := tc.parse(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(value)
			if err != nil || string(out) != tc.expected {
				t.Fatalf("canonical value %s, want %s (%v)", out, tc.expected, err)
			}
			if err := conformance.ValidateResult(schema, out); err != nil {
				t.Fatalf("canonical output violates raw official schema: %v", err)
			}
			for _, invalid := range []string{`null`, `"0xgg"`, `""`} {
				if _, err := tc.parse(json.RawMessage(invalid)); err == nil {
					t.Fatalf("invalid value accepted: %s", invalid)
				}
			}
		})
	}
}

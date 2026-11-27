package ethapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

// Input validity and expected representation are independent literals. A
// valid output cannot excuse a constructor accepting an invalid input.
func TestGeneratedScalarsValidateBeforeRepresentation(t *testing.T) {
	for _, tc := range []struct {
		name, raw, expected, pattern string
		parse                        func(json.RawMessage) (string, error)
	}{
		{"hash lowercase", `"0x` + strings.Repeat("a", 64) + `"`, `"0x` + strings.Repeat("a", 64) + `"`, `^0x[0-9a-f]{64}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHash(raw)
			return string(value), err
		}},
		{"hash uppercase prefix", `"0X` + strings.Repeat("a", 64) + `"`, "", `^0x[0-9a-f]{64}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHash(raw)
			return string(value), err
		}},
		{"hash uppercase digits", `"0x` + strings.Repeat("A", 64) + `"`, "", `^0x[0-9a-f]{64}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHash(raw)
			return string(value), err
		}},
		{"address uppercase digits", `"0x` + strings.Repeat("A", 40) + `"`, `"0x` + strings.Repeat("a", 40) + `"`, `^0x[0-9a-fA-F]{40}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewAddress(raw)
			return string(value), err
		}},
		{"address uppercase prefix", `"0X` + strings.Repeat("A", 40) + `"`, "", `^0x[0-9a-fA-F]{40}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewAddress(raw)
			return string(value), err
		}},
		{"byte leading zero", `"0x04"`, `"0x4"`, `^0x[0-9a-f]{1,2}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHexByte(raw)
			return string(value), err
		}},
		{"byte zero", `"0x00"`, `"0x0"`, `^0x[0-9a-f]{1,2}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHexByte(raw)
			return string(value), err
		}},
		{"byte uppercase", `"0x4A"`, "", `^0x[0-9a-f]{1,2}$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewHexByte(raw)
			return string(value), err
		}},
		{"data leading zeros", `"0x0004"`, `"0x0004"`, `^0x([0-9a-f]{2})*$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewData(raw)
			return string(value), err
		}},
		{"data odd digits", `"0x4"`, "", `^0x([0-9a-f]{2})*$`, func(raw json.RawMessage) (string, error) {
			value, err := ethapi.NewData(raw)
			return string(value), err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := map[string]any{"type": "string", "pattern": tc.pattern}
			valid := tc.expected != ""
			if err := conformance.ValidateResult(schema, json.RawMessage(tc.raw)); (err == nil) != valid {
				t.Fatalf("input schema validity = %v, want %v: %v", err == nil, valid, err)
			}
			value, err := tc.parse(json.RawMessage(tc.raw))
			if (err == nil) != valid {
				t.Fatalf("constructor validity = %v, want %v: %v", err == nil, valid, err)
			}
			if !valid {
				return
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

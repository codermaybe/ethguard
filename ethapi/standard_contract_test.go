package ethapi_test

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func TestGeneratedConstructorsMatchEffectiveSchemas(t *testing.T) {
	bundle, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]map[string]any)
	for method, raw := range bundle.Results {
		var schema map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&schema); err != nil {
			t.Fatal(err)
		}
		schemas[method] = schema
	}
	errorSchema := resultSchemaAt(t, schemas["eth_simulateV1"], "items", "properties", "calls", "items", "oneOf", "0", "properties", "error", "oneOf", "0")
	cases := []struct {
		name, raw string
		schema    map[string]any
		parse     func(json.RawMessage) error
		valid     bool
	}{
		{"quantity canonical", `"0xaf"`, schemas["eth_getBalance"], func(raw json.RawMessage) error { _, err := ethapi.NewGetBalanceResult(raw); return err }, true},
		{"quantity uppercase", `"0XAF"`, schemas["eth_getBalance"], func(raw json.RawMessage) error { _, err := ethapi.NewGetBalanceResult(raw); return err }, false},
		{"decimal sign", `"+01"`, schemas["net_version"], func(raw json.RawMessage) error { _, err := ethapi.NewVersionResult(raw); return err }, false},
		{"decimal zero", `"0"`, schemas["net_version"], func(raw json.RawMessage) error { _, err := ethapi.NewVersionResult(raw); return err }, true},
		{"closed result extra", `{"vendor":1}`, schemas["eth_syncing"], func(raw json.RawMessage) error { _, err := ethapi.NewSyncingResult(raw); return err }, false},
		{"closed result exact property", `{"currentBlock":"0x1"}`, schemas["eth_syncing"], func(raw json.RawMessage) error { _, err := ethapi.NewSyncingResult(raw); return err }, true},
		{"required property wrong case", `{"CODE":3,"message":"execution reverted"}`, errorSchema, func(raw json.RawMessage) error { _, err := ethapi.NewSimulateErrorRevert(raw); return err }, false},
		{"open result similarly named extra", `{"code":3,"CODE":4,"message":"execution reverted"}`, errorSchema, func(raw json.RawMessage) error { _, err := ethapi.NewSimulateErrorRevert(raw); return err }, true},
		{"integer constant decimal", `{"code":3.0,"message":"execution reverted"}`, errorSchema, func(raw json.RawMessage) error { _, err := ethapi.NewSimulateErrorRevert(raw); return err }, true},
		{"integer constant exponent", `{"code":3e0,"message":"execution reverted"}`, errorSchema, func(raw json.RawMessage) error { _, err := ethapi.NewSimulateErrorRevert(raw); return err }, true},
		{"integer constant exact mismatch", `{"code":3.000000000000000000000001,"message":"execution reverted"}`, errorSchema, func(raw json.RawMessage) error { _, err := ethapi.NewSimulateErrorRevert(raw); return err }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			if err := conformance.ValidateResult(tc.schema, raw); (err == nil) != tc.valid {
				t.Fatalf("effective schema validity = %v, want %v: %v", err == nil, tc.valid, err)
			}
			if err := tc.parse(raw); (err == nil) != tc.valid {
				t.Fatalf("generated constructor validity = %v, want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func resultSchemaAt(t *testing.T, value any, path ...string) map[string]any {
	t.Helper()
	for _, part := range path {
		switch node := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = node[part]
			if !ok {
				t.Fatalf("schema member %q is missing", part)
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				t.Fatalf("invalid schema branch index %q", part)
			}
			value = node[index]
		default:
			t.Fatalf("schema path %q traverses %T", part, value)
		}
	}
	schema, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema path does not select an object: %T", value)
	}
	return schema
}

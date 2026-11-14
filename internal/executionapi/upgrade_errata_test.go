package executionapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func TestErrataReviewedInventoryAndScope(t *testing.T) {
	original, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range effective.Errata {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"getlogs-empty-array-overlap", "simulate-block-state-calls-items", "transaction-query-pending-context"}) {
		t.Fatalf("correction inventory: %v", ids)
	}
	for method, schema := range original.Results {
		if method != "eth_getLogs" && method != "eth_getTransactionByHash" && !bytes.Equal(schema, effective.Results[method]) {
			t.Fatalf("unrelated result %s changed", method)
		}
	}
	for method, params := range original.Requests {
		if method != "eth_simulateV1" && !reflect.DeepEqual(params, effective.Requests[method]) {
			t.Fatalf("unrelated request %s changed", method)
		}
	}
	before := decodeSchemaObject(t, original.Requests["eth_simulateV1"][0].Schema)
	after := decodeSchemaObject(t, effective.Requests["eth_simulateV1"][0].Schema)
	blocks := after["properties"].(map[string]any)["blockStateCalls"].(map[string]any)
	item := blocks["items"].(map[string]any)
	if item["type"] != "object" || len(item) != 2 {
		t.Fatalf("unexpected item envelope: %+v", item)
	}
	blocks["properties"] = item["properties"]
	delete(blocks, "items")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("items correction changed something beyond moving properties into object items")
	}
	want := decodeSchemaObject(t, original.Results["eth_getLogs"])
	logs := decodeSchemaObject(t, effective.Results["eth_getLogs"])
	logs["oneOf"] = logs["anyOf"]
	delete(logs, "anyOf")
	if !reflect.DeepEqual(want, logs) {
		t.Fatal("getLogs correction changed branch contents")
	}
	for _, e := range effective.Errata {
		t.Logf("%s: patch=%s input=%s output=%s", e.ID, e.SHA256, e.InputSchemaSHA256, e.OutputSchemaSHA256)
	}
}

func TestSimulateItemsCorrectionEnforcesArrayOfObjectsAndOverrideConstraints(t *testing.T) {
	original, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	schema := decodeSchemaObject(t, effective.Requests["eth_simulateV1"][0].Schema)
	source := decodeSchemaObject(t, original.Requests["eth_simulateV1"][0].Schema)
	if err := conformance.ValidateResult(source, json.RawMessage(`{"blockStateCalls":[1]}`)); err != nil {
		t.Fatalf("upstream now constrains array items; retire the erratum: %v", err)
	}
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"blockStateCalls":[]}`, true},
		{`{"blockStateCalls":[{}]}`, true},
		{`{"blockStateCalls":[{"stateOverrides":{"0x0000000000000000000000000000000000000001":{"balance":"0x1"}}}]}`, true},
		{`{"blockStateCalls":[{"blockOverrides":{"prevRandao":"0x0000000000000000000000000000000000000000000000000000000000000001"},"calls":[{"blobVersionedHashes":["0x0000000000000000000000000000000000000000000000000000000000000001"]}]}]}`, true},
		{`{"blockStateCalls":[1]}`, false},
		{`{"blockStateCalls":[null]}`, false},
		{`{"blockStateCalls":{}}`, false},
		{`{"blockStateCalls":[{"calls":{}}]}`, false},
		{`{"blockStateCalls":[{"stateOverrides":{"bad":{"balance":"0x1"}}}]}`, false},
		{`{"blockStateCalls":[{"stateOverrides":{"0x0000000000000000000000000000000000000001":{"state":{},"stateDiff":{}}}}]}`, false},
		{`{"blockStateCalls":[{"blockOverrides":{"prevRandao":"0x1"}}]}`, false},
		{`{"blockStateCalls":[{"calls":[{"blobVersionedHashes":"0x1"}]}]}`, false},
	} {
		if err := conformance.ValidateResult(schema, json.RawMessage(tc.raw)); (err == nil) != tc.valid {
			t.Errorf("valid=%v: %s: %v", tc.valid, tc.raw, err)
		}
	}
	// Source provenance stays independent of the effective request schema.
	if bytes.Equal(original.Requests["eth_simulateV1"][0].Schema, effective.Requests["eth_simulateV1"][0].Schema) || spec.Version() == "" {
		t.Fatal("effective correction was not applied")
	}
}

package executionapi

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/codermaybe/ethguard/internal/spec"
)

func pendingErratum(t *testing.T) (erratum, []byte) {
	t.Helper()
	raw, err := fs.ReadFile(spec.ErrataFS(), "errata/transaction-query-pending-context.json")
	if err != nil {
		t.Fatal(err)
	}
	var e erratum
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	return e, raw
}

func testErrataFiles(t *testing.T, e erratum) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{"errata/pending.json": &fstest.MapFile{Data: mustJSON(t, e)}}
}

func decodeSchemaObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestErrataOnlyChangesFourRequiredQueryValues(t *testing.T) {
	before := append([]byte(nil), spec.MethodsJSON()...)
	original, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	e, raw := pendingErratum(t)
	if err := applyErrata(&effective, spec.SourceJSON(), fstest.MapFS{"errata/pending.json": &fstest.MapFile{Data: raw}}); err != nil {
		t.Fatal(err)
	}
	method := "eth_getTransactionByHash"
	if len(effective.Errata) != 1 || effective.Errata[0].ID != "transaction-query-pending-context" {
		t.Fatalf("audit = %+v", effective.Errata)
	}
	audit := effective.Errata[0]
	if audit.SHA256 != digest(raw) || audit.InputSchemaSHA256 != digest(original.Results[method]) || audit.OutputSchemaSHA256 != digest(effective.Results[method]) {
		t.Fatalf("audit does not identify actual inputs and output: %+v", audit)
	}
	if audit.InputSchemaSHA256 != e.InputSchemaSHA256 || audit.OutputSchemaSHA256 != e.OutputSchemaSHA256 {
		t.Fatal("applied hashes differ from reviewed correction")
	}
	want := decodeSchemaObject(t, original.Results[method])
	actual := decodeSchemaObject(t, effective.Results[method])
	props := actual["oneOf"].([]any)[1].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		wrapper := props[key].(map[string]any)
		branches, ok := wrapper["oneOf"].([]any)
		if !ok || len(branches) != 2 || !reflect.DeepEqual(branches[0], map[string]any{"type": "null"}) {
			t.Fatalf("%s: wrong nullable wrapper %+v", key, wrapper)
		}
		props[key] = branches[1]
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("correction changed something beyond the four nullable wrappers, including required fields")
	}
	for name, schema := range original.Results {
		if name != method && !bytes.Equal(schema, effective.Results[name]) {
			t.Fatalf("unrelated result %s changed", name)
		}
	}
	if !reflect.DeepEqual(original.Requests, effective.Requests) || !bytes.Equal(before, spec.MethodsJSON()) {
		t.Fatal("request or raw source was changed")
	}
	t.Logf("%s: source=%s input=%s patch=%s output=%s", e.ID, e.SourceCommit, audit.InputSchemaSHA256, audit.SHA256, audit.OutputSchemaSHA256)
}

func TestErrataRejectSourceDriftAndInvalidAuditAtomically(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*Bundle, *erratum)
	}{
		{"source version", "source pin changed", func(_ *Bundle, e *erratum) { e.SourceVersion = "future" }},
		{"source commit", "source pin changed", func(_ *Bundle, e *erratum) { e.SourceCommit = strings.Repeat("f", 40) }},
		{"required changed", "input schema digest", func(b *Bundle, _ *erratum) {
			b.Results["eth_getTransactionByHash"] = bytes.Replace(b.Results["eth_getTransactionByHash"], []byte(`"blockTimestamp",`), []byte(`"unrelated",`), 1)
		}},
		{"new keyword", "input schema digest", func(b *Bundle, _ *erratum) {
			raw := b.Results["eth_getTransactionByHash"]
			b.Results["eth_getTransactionByHash"] = append([]byte(`{"newKeyword":true,`), raw[1:]...)
		}},
		{"upstream fixed", "input schema digest", func(b *Bundle, _ *erratum) {
			fixed, err := Read()
			if err != nil {
				t.Fatal(err)
			}
			b.Results["eth_getTransactionByHash"] = fixed.Results["eth_getTransactionByHash"]
		}},
		{"output changed", "output schema digest", func(_ *Bundle, e *erratum) { e.OutputSchemaSHA256 = strings.Repeat("0", 64) }},
		{"test failed", "apply RFC 6902 patch", func(_ *Bundle, e *erratum) {
			e.Patch = json.RawMessage(`[{"op":"test","path":"/oneOf/1/properties/blockHash/type","value":"number"}]`)
		}},
		{"negative index", "apply RFC 6902 patch", func(_ *Bundle, e *erratum) {
			e.Patch = json.RawMessage(`[{"op":"replace","path":"/oneOf/-1","value":{"type":"null"}}]`)
		}},
		{"missing remove", "apply RFC 6902 patch", func(_ *Bundle, e *erratum) { e.Patch = json.RawMessage(`[{"op":"remove","path":"/missing"}]`) }},
		{"missing ancestor", "apply RFC 6902 patch", func(_ *Bundle, e *erratum) {
			e.Patch = json.RawMessage(`[{"op":"add","path":"/missing/child","value":true}]`)
		}},
		{"noop", "does not change the schema document", func(_ *Bundle, e *erratum) {
			e.Patch = json.RawMessage(`[{"op":"test","path":"/oneOf/0/type","value":"null"}]`)
		}},
		{"empty patch", "must contain operations", func(_ *Bundle, e *erratum) { e.Patch = json.RawMessage(`[]`) }},
		{"missing reason", "incomplete audit metadata", func(_ *Bundle, e *erratum) { e.Reason = "" }},
		{"missing exit", "incomplete audit metadata", func(_ *Bundle, e *erratum) { e.RetireWhen = "" }},
		{"missing basis", "incomplete audit metadata", func(_ *Bundle, e *erratum) { e.References = nil }},
		{"invalid basis", "absolute HTTPS URL", func(_ *Bundle, e *erratum) { e.References = []string{"local-note"} }},
		{"missing target", "does not exist", func(_ *Bundle, e *erratum) { e.Target.Method = "eth_missing" }},
		{"bad target kind", "target part", func(_ *Bundle, e *erratum) { e.Target.Part = "response" }},
		{"request index absent", "parameter does not exist", func(_ *Bundle, e *erratum) { e.Target.Part = "request" }},
		{"result parameter", "cannot select", func(_ *Bundle, e *erratum) { i := 0; e.Target.Parameter = &i }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := ReadOriginal()
			if err != nil {
				t.Fatal(err)
			}
			e, _ := pendingErratum(t)
			tc.change(&bundle, &e)
			before := mustJSON(t, bundle)
			err = applyErrata(&bundle, spec.SourceJSON(), testErrataFiles(t, e))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
			if !bytes.Equal(before, mustJSON(t, bundle)) {
				t.Fatal("failed correction partially mutated the view")
			}
		})
	}
}

func TestErrataRejectDuplicateApplicationAndLaterFailure(t *testing.T) {
	bundle, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, bundle)
	if err := applyErrata(&bundle, spec.SourceJSON(), spec.ErrataFS()); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("duplicate error = %v", err)
	}
	if !bytes.Equal(before, mustJSON(t, bundle)) {
		t.Fatal("duplicate correction mutated view")
	}
	e, _ := pendingErratum(t)
	files := testErrataFiles(t, e)
	e.ID = "later-invalid"
	e.InputSchemaSHA256 = strings.Repeat("0", 64)
	files["errata/z-later.json"] = &fstest.MapFile{Data: mustJSON(t, e)}
	bundle, err = ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	before = mustJSON(t, bundle)
	if err := applyErrata(&bundle, spec.SourceJSON(), files); err == nil {
		t.Fatal("accepted failed second correction")
	}
	if !bytes.Equal(before, mustJSON(t, bundle)) {
		t.Fatal("later failure committed the first correction")
	}
}

func TestErrataRequestTargetKeepsParameterDescriptorsAndResults(t *testing.T) {
	bundle, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := pendingErratum(t)
	index := 0
	e.Target = schemaTarget{Method: "eth_call", Part: "request", Parameter: &index}
	const original = `{"type":"object","properties":{"old":{"type":"string"}}}`
	const expected = `{"type":"object","properties":{"new":{"type":"string"}}}`
	bundle.Requests["eth_call"][0].Schema = json.RawMessage(original)
	paramsBefore := append([]Parameter(nil), bundle.Requests["eth_call"]...)
	resultsBefore := mustJSON(t, bundle.Results)
	e.InputSchemaSHA256 = digest([]byte(original))
	e.OutputSchemaSHA256 = digest([]byte(expected))
	e.Patch = json.RawMessage(`[{"op":"test","path":"/properties/old/type","value":"string"},{"op":"move","from":"/properties/old","path":"/properties/new"}]`)
	if err := applyErrata(&bundle, spec.SourceJSON(), testErrataFiles(t, e)); err != nil {
		t.Fatal(err)
	}
	if string(bundle.Requests["eth_call"][0].Schema) != expected {
		t.Fatalf("effective request = %s", bundle.Requests["eth_call"][0].Schema)
	}
	bundle.Requests["eth_call"][0].Schema = json.RawMessage(original)
	if !reflect.DeepEqual(paramsBefore, bundle.Requests["eth_call"]) || !bytes.Equal(resultsBefore, mustJSON(t, bundle.Results)) {
		t.Fatal("request correction changed descriptors, other parameters, or result schemas")
	}
}

func TestErrataRejectAmbiguousAuditDocuments(t *testing.T) {
	_, raw := pendingErratum(t)
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"unknown metadata", append([]byte(`{"unexpected":true,`), raw[1:]...), "unknown field"},
		{"duplicate metadata", append([]byte(`{"id":"hidden",`), raw[1:]...), "duplicate object key"},
		{"trailing JSON", append(append([]byte(nil), raw...), []byte(`{}`)...), "trailing data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := ReadOriginal()
			if err != nil {
				t.Fatal(err)
			}
			before := mustJSON(t, bundle)
			files := fstest.MapFS{"errata/bad.json": &fstest.MapFile{Data: tc.raw}}
			if err := applyErrata(&bundle, spec.SourceJSON(), files); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
			if !bytes.Equal(before, mustJSON(t, bundle)) {
				t.Fatal("invalid audit JSON mutated the view")
			}
		})
	}
}

func TestErrataCanRetireTheLastCorrection(t *testing.T) {
	bundle, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	before := mustJSON(t, bundle)
	// A tracked README keeps the embedded directory present when no JSON
	// corrections remain. No tombstone patch or weakened precondition is needed.
	files := fstest.MapFS{"errata/README.md": &fstest.MapFile{Data: []byte("No remaining corrections.")}}
	if err := applyErrata(&bundle, spec.SourceJSON(), files); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustJSON(t, bundle)) || len(bundle.Errata) != 0 {
		t.Fatal("empty errata inventory changed the original schema")
	}
}

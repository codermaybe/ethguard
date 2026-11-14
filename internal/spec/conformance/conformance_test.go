package conformance

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/codermaybe/ethguard/internal/spec"
)

func TestEmbeddedExecutionAPIVectors(t *testing.T) {
	report := Run(spec.VectorsFS(), spec.MethodsJSON(), spec.Version())
	if len(report.Issues) != 0 {
		t.Fatalf("embedded conformance issues: %v", report.Issues)
	}
	if report.TotalVectors == 0 || report.ValidatedResult == 0 {
		t.Fatalf("empty conformance report: %#v", report)
	}
	if report.BundleVersion != spec.Version() {
		t.Fatalf("bundle version %q, want %q", report.BundleVersion, spec.Version())
	}
	if len(report.Uncovered) != 0 {
		t.Fatalf("unexpected uncovered official vector methods: %#v", report.Uncovered)
	}
}

func TestRunRejectsPinnedVersionDrift(t *testing.T) {
	report := Run(spec.VectorsFS(), spec.MethodsJSON(), "future-release")
	if len(report.Issues) != 1 || !strings.Contains(report.Issues[0].Detail, "does not match pin") {
		t.Fatalf("version drift was not reported: %#v", report.Issues)
	}
}

func TestValidateSchemaChecksRequiredPatternAndUnknownFields(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"required":             []any{"id"},
		"additionalProperties": false,
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "pattern": `^0x[0-9a-f]+$`},
		},
	}
	valid := map[string]any{"id": "0x1"}
	if err := validateSchema(schema, valid, "$", 0); err != nil {
		t.Fatal(err)
	}
	for _, value := range []map[string]any{
		{"id": "1"},
		{"other": "0x1"},
	} {
		if err := validateSchema(schema, value, "$", 0); err == nil {
			t.Fatalf("invalid value accepted: %#v", value)
		}
	}
}

func TestParseVectorPairsEveryRequestAndResponseInOrder(t *testing.T) {
	const path = "nested/sample.io"
	data := "// comments do not identify a method\n" +
		"  >>  {\"method\" : \"eth_getBlockByNumber\", \"id\":9}\n" +
		">>\t{\"method\":\"eth_getBlockByNumberExtra\",\"id\":1}\n" +
		"<< {\"id\":1, \"result\":null}\n" +
		" <<\t{\"id\":9,\"error\":{\"code\":-1,\"message\":\"failure\"}}\n" +
		">>{\"method\":\"eth_getTransactionByHash\"}\n" +
		"<< {\"result\":{\"value\":1}}\n"
	fsys := fstest.MapFS{path: &fstest.MapFile{Data: []byte(data)}}
	vectors, err := ParseVector(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 {
		t.Fatalf("parsed %d pairs, want 3", len(vectors))
	}
	methods := []string{"eth_getBlockByNumber", "eth_getBlockByNumberExtra", "eth_getTransactionByHash"}
	identities := []string{"nested/sample.io[0]", "nested/sample.io[1]", "nested/sample.io[2]"}
	for i, vector := range vectors {
		if vector.Path != path || vector.Index != i || vector.Identity() != identities[i] || vector.Method != methods[i] {
			t.Fatalf("pair %d identity/method = %#v", i, vector)
		}
		var request map[string]any
		if err := json.Unmarshal(vector.Request, &request); err != nil || request["method"] != methods[i] {
			t.Fatalf("pair %d lost request: %s (%v)", i, vector.Request, err)
		}
	}
	if string(vectors[0].Result) != "null" || vectors[0].Error != nil {
		t.Fatalf("null result must remain present: %#v", vectors[0])
	}
	if vectors[1].Result != nil || string(vectors[1].Error) != `{"code":-1,"message":"failure"}` {
		t.Fatalf("error response must not become a null result: %#v", vectors[1])
	}
	if string(vectors[2].Result) != `{"value":1}` || vectors[2].Error != nil {
		t.Fatalf("third response lost: %#v", vectors[2])
	}
}

func TestParseVectorRejectsMalformedFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "// no pairs\n", "at least one request and response"},
		{"request only", ">> {\"method\":\"x\"}\n", "at least one request and response"},
		{"count", ">> {\"method\":\"x\"}\n>> {\"method\":\"y\"}\n<< {\"result\":0}\n", "count mismatch: 2/1"},
		{"request JSON", ">> {\n<< {\"result\":0}\n", "sample.io[0]: request JSON"},
		{"response JSON", ">> {\"method\":\"x\"}\n<< {\n", "sample.io[0]: response JSON"},
		{"method missing", ">> {}\n<< {\"result\":null}\n", "sample.io[0]: request has no method"},
		{"result missing", ">> {\"method\":\"x\"}\n<< {\"id\":1}\n", "sample.io[0]: response has neither result nor error"},
		{"ambiguous response", ">> {\"method\":\"x\"}\n<< {\"result\":null,\"error\":{}}\n", "sample.io[0]: response has both result and error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"sample.io": &fstest.MapFile{Data: []byte(tc.data)}}
			if _, err := ParseVector(fsys, "sample.io"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseVector error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunUsesExactMethodsAndPairIdentities(t *testing.T) {
	bundle := []byte(`{"version":"test","results":{"eth_x":{"type":"string"},"eth_nullable":{"type":"null"}}}`)
	fsys := fstest.MapFS{
		"a/same.io": &fstest.MapFile{Data: []byte(">> {\"method\" : \"eth_x\"}\n>> {\"method\":\"eth_xExtra\"}\n>> {\"method\":\"eth_nullable\"}\n<< {\"result\":1}\n<< {\"result\":\"uncovered\"}\n<< {\"result\":null}\n")},
		"b/same.io": &fstest.MapFile{Data: []byte(">> {\"method\":\"eth_x\"}\n<< {\"error\":{\"code\":-1}}\n")},
	}
	report := Run(fsys, bundle, "test")
	if report.TotalVectors != 4 || report.ValidatedResult != 1 || report.ErrorResponses != 1 {
		t.Fatalf("incorrect pair accounting: %#v", report)
	}
	if len(report.Uncovered) != 1 || report.Uncovered[0] != "eth_xExtra" {
		t.Fatalf("method prefix was mistaken for exact match: %#v", report.Uncovered)
	}
	if len(report.Issues) != 1 || report.Issues[0].Path != "a/same.io[0]" || report.Issues[0].Method != "eth_x" {
		t.Fatalf("issue lost complete pair identity: %#v", report.Issues)
	}
}

func TestValidateResultRejectsInvalidOrTrailingJSON(t *testing.T) {
	schema := map[string]any{"type": "string"}
	for _, raw := range []string{`1`, `"valid" null`, `"unterminated`} {
		if err := ValidateResult(schema, json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid result accepted: %s", raw)
		}
	}
	if err := ValidateResult(schema, json.RawMessage(`"valid"`)); err != nil {
		t.Fatal(err)
	}
}

func TestDiffBundlesReportsMethodAndFieldChanges(t *testing.T) {
	before := []byte(`{"version":"old","results":{"eth_x":{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}}}`)
	after := []byte(`{"version":"new","results":{"eth_x":{"type":"object","required":[],"properties":{"value":{"type":"number"},"extra":{"type":"boolean"}}},"eth_y":{"type":"string"}}}`)
	diff, err := DiffBundles(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if diff.BeforeVersion != "old" || diff.AfterVersion != "new" || len(diff.Methods) != 2 {
		t.Fatalf("unexpected bundle diff: %#v", diff)
	}
	if diff.Methods[0].Method != "eth_x" || len(diff.Methods[0].Changed) != 2 {
		t.Fatalf("unexpected eth_x changes: %#v", diff.Methods[0])
	}
	if diff.Methods[1].Method != "eth_y" || !diff.Methods[1].Added {
		t.Fatalf("unexpected eth_y change: %#v", diff.Methods[1])
	}
}

func TestValidateMigrationRequiresExactChangesAndEvidence(t *testing.T) {
	before := []byte(`{"version":"old","results":{"eth_x":{"type":"string"}}}`)
	after := []byte(`{"version":"new","results":{"eth_x":{"type":"number"},"eth_y":{"type":"string"}}}`)
	diff, err := DiffBundles(before, after)
	if err != nil {
		t.Fatal(err)
	}
	missing := []byte(`{"beforeVersion":"old","afterVersion":"new","conformancePassed":true,"replayPassed":true,"changes":[]}`)
	if err := ValidateMigration(diff, missing); err == nil || !strings.Contains(err.Error(), "missing changes") {
		t.Fatalf("missing migration entries accepted: %v", err)
	}
	complete := []byte(`{"beforeVersion":"old","afterVersion":"new","conformancePassed":true,"replayPassed":true,"changes":[{"method":"eth_x","path":"$","kind":"changed"},{"method":"eth_y","path":"$","kind":"method-added"}]}`)
	if err := ValidateMigration(diff, complete); err != nil {
		t.Fatalf("complete migration rejected: %v", err)
	}
}

func TestValidateMigrationRejectsMissingReplayEvidence(t *testing.T) {
	diff, err := DiffBundles(
		[]byte(`{"version":"old","results":{"eth_x":{"type":"string"}}}`),
		[]byte(`{"version":"new","results":{"eth_x":{"type":"number"}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"beforeVersion":"old","afterVersion":"new","conformancePassed":true,"changes":[{"method":"eth_x","path":"$","kind":"changed"}]}`)
	if err := ValidateMigration(diff, manifest); err == nil || !strings.Contains(err.Error(), "replayPassed") {
		t.Fatalf("missing replay evidence accepted: %v", err)
	}
}

func TestValidateResultDistinguishesOneOfAndAnyOfOverlap(t *testing.T) {
	branches := []any{
		map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
	}
	for _, tc := range []struct {
		composition string
		valid       bool
	}{
		{"oneOf", false},
		{"anyOf", true},
	} {
		t.Run(tc.composition, func(t *testing.T) {
			schema := map[string]any{tc.composition: branches}
			err := ValidateResult(schema, json.RawMessage(`[]`))
			if (err == nil) != tc.valid {
				t.Fatalf("overlapping empty array: valid=%v, error=%v", tc.valid, err)
			}
			if err := ValidateResult(schema, json.RawMessage(`["hash"]`)); err != nil {
				t.Fatalf("single matching branch rejected: %v", err)
			}
			if err := ValidateResult(schema, json.RawMessage(`[false]`)); err == nil {
				t.Fatal("zero matching branches admitted")
			}
		})
	}
}

func TestValidateResultUsesExactDecimalBounds(t *testing.T) {
	schema := map[string]any{"type": "number", "minimum": json.Number("0"), "maximum": json.Number("1")}
	for _, raw := range []string{
		"0.30000000000000001",
		"0.99999999999999999999",
		"1e-999999999999999999999999999999999999",
		"0e999999999999999999999999999999999999",
		"-0",
	} {
		if err := ValidateResult(schema, json.RawMessage(raw)); err != nil {
			t.Errorf("exact in-range number rejected: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"1.00000000000000000001",
		"-0.00000000000000000001",
		"1e999999999999999999999999999999999999",
		"-1e-999999999999999999999999999999999999",
	} {
		if err := ValidateResult(schema, json.RawMessage(raw)); err == nil {
			t.Errorf("out-of-range number rounded into bounds: %s", raw)
		}
	}
}

func TestValidateResultUsesExactIntegerAndConstSemantics(t *testing.T) {
	integer := map[string]any{"type": "integer"}
	for _, raw := range []string{"1.0", "100e-2", "1e999999999999999999999999999999999999", "-0.0"} {
		if err := ValidateResult(integer, json.RawMessage(raw)); err != nil {
			t.Errorf("mathematical integer rejected: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{"1.00000000000000000001", "1e-999999999999999999999999999999999999"} {
		if err := ValidateResult(integer, json.RawMessage(raw)); err == nil {
			t.Errorf("fraction rounded to integer: %s", raw)
		}
	}
	constant := map[string]any{"const": json.Number("3")}
	if err := ValidateResult(constant, json.RawMessage("3e0")); err != nil {
		t.Fatalf("equivalent decimal const rejected: %v", err)
	}
	if err := ValidateResult(constant, json.RawMessage("3.00000000000000000001")); err == nil {
		t.Fatal("unequal decimal rounded to const")
	}
}

func TestRunPreservesExactNumericSchemaBounds(t *testing.T) {
	bundle := []byte(`{"version":"exact","results":{"x":{"type":"number","maximum":0.30000000000000001}}}`)
	fsys := fstest.MapFS{"number.io": &fstest.MapFile{Data: []byte(">> {\"method\":\"x\"}\n<< {\"result\":0.30000000000000002}\n")}}
	report := Run(fsys, bundle, "exact")
	if len(report.Issues) != 1 || report.Issues[0].Path != "number.io[0]" || !strings.Contains(report.Issues[0].Detail, "above maximum") {
		t.Fatalf("bundle bound lost precision: %#v", report)
	}
}

func TestUpgradeDiffIncludesAssertionVocabulary(t *testing.T) {
	for _, assertion := range []string{`"const":3`, `"required":["x"]`, `"not":{"required":["x"]}`, `"patternProperties":{"^x$":{"type":"string"}}`, `"uniqueItems":true`, `"futureAssertion":1`} {
		before := []byte(`{"version":"old","results":{"eth_x":{"type":"object"}}}`)
		after := []byte(`{"version":"new","results":{"eth_x":{"type":"object",` + assertion + `}}}`)
		diff, err := DiffBundles(before, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(diff.Methods) != 1 || len(diff.Methods[0].Changed) != 1 || diff.Methods[0].Changed[0].Path != "$" {
			t.Fatalf("assertion omitted from upgrade diff: %s: %+v", assertion, diff)
		}
	}
}

func TestVectorReaderRetainsBoundedOfflineLineLimit(t *testing.T) {
	files := fstest.MapFS{"large.io": &fstest.MapFile{Data: []byte(strings.Repeat("x", 32<<20))}}
	if _, err := ParseVector(files, "large.io"); err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("oversized offline line error = %v", err)
	}
}

func TestValidateResultArrayConstraintsAndPatternProperties(t *testing.T) {
	schema := map[string]any{
		"type": "array", "minItems": json.Number("1"), "maxItems": json.Number("2"), "uniqueItems": true,
		"items": map[string]any{"type": "object", "patternProperties": map[string]any{"^x$": map[string]any{"type": "string"}, "x": map[string]any{"enum": []any{"ok"}}}, "additionalProperties": false},
	}
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`[{"x":"ok"}]`, true}, {`[]`, false}, {`[{"x":"ok"},{"x":"ok"}]`, false}, {`[{"x":"bad"}]`, false}, {`[{"x":1}]`, false}, {`[{"y":"ok"}]`, false}, {`[{},{"x":"ok"},{}]`, false},
	} {
		if err := ValidateResult(schema, json.RawMessage(tc.raw)); (err == nil) != tc.valid {
			t.Errorf("valid=%v: %s: %v", tc.valid, tc.raw, err)
		}
	}
}

func TestBooleanAndAdditionalPropertySchemasRemainAssertions(t *testing.T) {
	for _, tc := range []struct {
		schema, value string
		valid         bool
	}{
		{`false`, `1`, false}, {`true`, `1`, true},
		{`{"not":false}`, `1`, true}, {`{"not":true}`, `1`, false},
		{`{"type":"object","additionalProperties":{"type":"string"}}`, `{"x":"ok"}`, true},
		{`{"type":"object","additionalProperties":{"type":"string"}}`, `{"x":1}`, false},
		{`{"type":"array","items":false}`, `[]`, true},
		{`{"type":"array","items":false}`, `[1]`, false},
	} {
		var schema any
		if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
			t.Fatal(err)
		}
		if err := ValidateResult(schema, json.RawMessage(tc.value)); (err == nil) != tc.valid {
			t.Errorf("%s / %s: %v", tc.schema, tc.value, err)
		}
	}
	diff, err := DiffBundles([]byte(`{"version":"old","results":{"eth_x":{"type":"array","items":true}}}`), []byte(`{"version":"new","results":{"eth_x":{"type":"array","items":false}}}`))
	if err != nil || len(diff.Methods) != 1 || len(diff.Methods[0].Changed) != 1 || diff.Methods[0].Changed[0].Path != "$/*" {
		t.Fatalf("boolean item assertion omitted: %+v (%v)", diff, err)
	}
}

func TestUpgradeDiffPreservesExactAssertionNumbers(t *testing.T) {
	before := []byte(`{"version":"old","results":{"eth_x":{"type":"number","const":9007199254740992}}}`)
	after := []byte(`{"version":"new","results":{"eth_x":{"type":"number","const":9007199254740993}}}`)
	diff, err := DiffBundles(before, after)
	if err != nil || len(diff.Methods) != 1 || len(diff.Methods[0].Changed) != 1 {
		t.Fatalf("exact numeric assertion omitted: %+v (%v)", diff, err)
	}
	if _, err := DiffBundles(before, append(after, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing bundle data accepted")
	}
}

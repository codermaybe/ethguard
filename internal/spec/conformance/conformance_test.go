package conformance

import (
	"strings"
	"testing"
	"testing/fstest"

	"ethguard/internal/spec"
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
	if len(report.Uncovered) != 1 || report.Uncovered[0] != "eth_sendRawTransaction" {
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

func TestParseVectorRequiresRequestAndResponse(t *testing.T) {
	fsys := fstest.MapFS{"sample.io": &fstest.MapFile{Data: []byte("// comment\n>> {\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"x\"}\n<< {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":null}\n")}}
	vectors, err := parseVector(fsys, "sample.io")
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 {
		t.Fatalf("parsed vectors = %#v", vectors)
	}
	vector := vectors[0]
	if vector.Method != "x" || string(vector.Result) != "null" {
		t.Fatalf("parsed vector = %#v", vector)
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

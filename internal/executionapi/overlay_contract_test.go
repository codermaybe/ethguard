package executionapi

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/codermaybe/ethguard/internal/spec"
)

func allOverlays(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(spec.ErrataFS(), "errata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := "errata/" + entry.Name()
		raw, err := fs.ReadFile(spec.ErrataFS(), name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = &fstest.MapFile{Data: raw}
	}
	return out
}
func TestOverlayDiagnosticRetirementAndRootIdentity(t *testing.T) {
	original, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	original.Results["eth_getCode"] = effective.Results["eth_getCode"]
	diagnostic, statuses, err := EvaluateOverlays(original, spec.SourceJSON(), spec.ErrataFS())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, status := range statuses {
		for _, target := range status.Targets {
			if target.Source.Method == "eth_getCode" && target.Source.Part == "result" {
				found = true
				if target.Status != "already-satisfied" || target.OriginalRootSHA256 != digest(original.Results["eth_getCode"]) || target.InputRootSHA256 != target.OutputRootSHA256 {
					t.Fatalf("bad retirement audit: %+v", target)
				}
			}
		}
	}
	if !found || len(diagnostic.Results) == 0 {
		t.Fatal("missing retirement diagnostic")
	}
	before := mustJSON(t, original)
	if err := applyErrata(&original, spec.SourceJSON(), spec.ErrataFS()); err == nil || !strings.Contains(err.Error(), "already satisfied") {
		t.Fatalf("normal Read accepted retirement: %v", err)
	}
	if !bytes.Equal(before, mustJSON(t, original)) {
		t.Fatal("retirement mutated original")
	}
}
func TestOverlayDependencyAndCrossRuleConflicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]*fstest.MapFile)
		want   string
	}{
		{"missing dependency", func(files map[string]*fstest.MapFile) {
			var e erratum
			if err := json.Unmarshal(files["errata/data-whole-bytes.json"].Data, &e); err != nil {
				t.Fatal(err)
			}
			e.DependsOn = []string{"missing"}
			files["errata/data-whole-bytes.json"].Data = mustJSON(t, e)
		}, "missing overlay dependency"},
		{"cycle", func(files map[string]*fstest.MapFile) {
			var e erratum
			if err := json.Unmarshal(files["errata/getlogs-empty-array-overlap.json"].Data, &e); err != nil {
				t.Fatal(err)
			}
			e.DependsOn = []string{"data-whole-bytes"}
			files["errata/getlogs-empty-array-overlap.json"].Data = mustJSON(t, e)
		}, "dependency cycle"},
		{"undeclared overlap", func(files map[string]*fstest.MapFile) {
			var e erratum
			if err := json.Unmarshal(files["errata/data-whole-bytes.json"].Data, &e); err != nil {
				t.Fatal(err)
			}
			e.DependsOn = nil
			files["errata/data-whole-bytes.json"].Data = mustJSON(t, e)
		}, "conflicting overlay writes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := allOverlays(t)
			tc.change(files)
			bundle, err := ReadOriginal()
			if err != nil {
				t.Fatal(err)
			}
			before := mustJSON(t, bundle)
			if err := applyErrata(&bundle, spec.SourceJSON(), files); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%s", err, tc.want)
			}
			if !bytes.Equal(before, mustJSON(t, bundle)) {
				t.Fatal("failure changed source")
			}
		})
	}
}
func TestDataEscapeCheckIsSelectedExplicitAndStaleSafe(t *testing.T) {
	bundle := Bundle{Results: map[string]json.RawMessage{"eth_a": json.RawMessage(`{"type":"object","properties":{"new":{"title":"new DATA","type":"string","pattern":"^0x[0-9a-f]*$"}}}`), "eth_unselected": json.RawMessage(`{"type":"string","pattern":"^0x[0-9a-f]*$"}`)}}
	selected := []SchemaSelection{{Source: SchemaRef{Method: "eth_a", Part: "result"}}}
	if err := ValidateSelectedData(bundle, selected); err == nil || !strings.Contains(err.Error(), "eth_a.result/properties/new") {
		t.Fatalf("unreviewed field escaped: %v", err)
	}
	source := SchemaRef{Method: "eth_a", Part: "result", Pointer: "/properties/new"}
	raw, err := SelectSchema(bundle, SchemaSelection{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	bundle.DataExceptions = []DataException{{Source: source, Identity: raw, Reason: "reviewed non-DATA domain", RetireWhen: "upstream clarifies domain"}}
	if err := ValidateSelectedData(bundle, selected); err != nil {
		t.Fatal(err)
	}
	bundle.Results["eth_a"] = bytes.ReplaceAll(bundle.Results["eth_a"], []byte(DataOriginalPattern), []byte(DataWholeBytePattern))
	if err := ValidateSelectedData(bundle, selected); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale exception accepted: %v", err)
	}
}
func TestOverlayAllowsFormattingDriftButChecksLocalSemantics(t *testing.T) {
	original, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range original.Results {
		original.Results[name] = append(append([]byte(nil), raw...), ' ')
	}
	if err := applyErrata(&original, spec.SourceJSON(), spec.ErrataFS()); err != nil {
		t.Fatalf("format-only drift required manual root hashes: %v", err)
	}
}

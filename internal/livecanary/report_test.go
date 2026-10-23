package livecanary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func reportExample(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "design", "live-canary", "report.example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report example: %v", err)
	}
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatalf("decode report example: %v", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		t.Fatal("report example is not an object")
	}
	return object
}

func TestReportSchemaRefsResolve(t *testing.T) {
	schema := loadReportSchema(t)
	var refs []string
	collectSchemaRefs(schema.root, &refs)
	if len(refs) == 0 {
		t.Fatal("schema has no $ref entries")
	}
	for _, ref := range refs {
		if _, err := schema.resolve(ref); err != nil {
			t.Errorf("unresolved $ref %q: %v", ref, err)
		}
	}
}

func TestReportSchemaAcceptsExample(t *testing.T) {
	schema := loadReportSchema(t)
	example := reportExample(t)
	if err := schema.validate(example); err != nil {
		t.Fatalf("schema rejected report.example.json: %v", err)
	}
	data, err := json.Marshal(example)
	if err != nil {
		t.Fatalf("marshal example: %v", err)
	}
	report, err := ParseReport(data)
	if err != nil {
		t.Fatalf("ParseReport rejected the example: %v", err)
	}
	roundTrip, err := report.Marshal()
	if err != nil {
		t.Fatalf("Marshal rejected the parsed example: %v", err)
	}
	if _, err := ParseReport(roundTrip); err != nil {
		t.Fatalf("round-tripped report does not parse: %v", err)
	}
}

func TestReportSchemaRequiresEveryTopLevelField(t *testing.T) {
	schema := loadReportSchema(t)
	required, ok := schema.root["required"].([]any)
	if !ok {
		t.Fatal("schema has no required list")
	}
	example := reportExample(t)
	for _, raw := range required {
		name := raw.(string)
		t.Run(name, func(t *testing.T) {
			clone := cloneJSONObject(t, example)
			delete(clone, name)
			if err := schema.validate(clone); err == nil {
				t.Fatalf("schema accepted a report without %q", name)
			}
			data, err := json.Marshal(clone)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, err := ParseReport(data); err == nil {
				t.Fatalf("ParseReport accepted a report without %q", name)
			}
		})
	}
}

func TestReportRejectsInconsistentEvidence(t *testing.T) {
	schema := loadReportSchema(t)
	tests := []struct {
		name         string
		semanticOnly bool
		mutate       func(*testing.T, map[string]any)
	}{
		{"unknown top-level field", false, func(_ *testing.T, report map[string]any) {
			report["ai_filled"] = true
		}},
		{"failed state without reasons", false, func(_ *testing.T, report map[string]any) {
			report["state"] = "failed"
		}},
		{"passed state with reason", false, func(_ *testing.T, report map[string]any) {
			report["state_reasons"] = []any{map[string]any{
				"category": "code_error", "step_id": nil, "detail": "broken",
			}}
		}},
		{"reason category outside closed set", false, func(_ *testing.T, report map[string]any) {
			report["state"] = "inconclusive"
			report["state_reasons"] = []any{map[string]any{
				"category": "because", "step_id": nil, "detail": "broken",
			}}
		}},
		{"wrong plan hash", false, func(_ *testing.T, report map[string]any) {
			report["plan_sha256"] = "0000000000000000000000000000000000000000000000000000000000000000"
		}},
		{"zero start", false, func(_ *testing.T, report map[string]any) {
			report["started_at"] = "0001-01-01T00:00:00Z"
		}},
		{"reference step not plan aligned", false, func(_ *testing.T, report map[string]any) {
			report["reference_step_id"] = "pinned.block"
		}},
		{"passed run missing a step", false, func(_ *testing.T, report map[string]any) {
			steps := arrayField(t, report, "steps")
			report["steps"] = steps[:len(steps)-1]
		}},
		{"step id out of order", true, func(t *testing.T, report map[string]any) {
			steps := arrayField(t, report, "steps")
			steps[0], steps[1] = steps[1], steps[0]
		}},
		{"decision denominator mismatch", true, func(t *testing.T, report map[string]any) {
			decisionField(t, report, 1)["n"] = json.Number("2")
		}},
		{"decision duplicate disposition", true, func(t *testing.T, report map[string]any) {
			decision := decisionField(t, report, 0)
			decision["reject"] = []any{"node-01"}
		}},
		{"representative not first supporter", true, func(t *testing.T, report map[string]any) {
			decisionField(t, report, 0)["representative"] = "node-02"
		}},
		{"successful decision without digest", true, func(t *testing.T, report map[string]any) {
			decisionField(t, report, 0)["value_sha256"] = ""
		}},
		{"unknown decision without category", true, func(t *testing.T, report map[string]any) {
			decision := decisionField(t, report, 0)
			decision["status"] = "unknown"
			decision["representative"] = ""
			decision["value_sha256"] = ""
			decision["value_kind"] = ""
		}},
		{"reference without hash", true, func(t *testing.T, report map[string]any) {
			referenceField(t, report, 1)["hash"] = ""
		}},
		{"reference unpartitioned participant", true, func(t *testing.T, report map[string]any) {
			reference := referenceField(t, report, 1)
			reference["reject"] = []any{"node-01"}
		}},
		{"typed absence with hash", true, func(t *testing.T, report map[string]any) {
			typedField(t, report, 5)["hash"] = "0x8f3a1c2d4e5f60718293a4b5c6d7e8f90123456789abcdef0123456789abcdef"
		}},
		{"typed block without height", true, func(t *testing.T, report map[string]any) {
			typedField(t, report, 4)["height"] = nil
		}},
		{"source health outside category", false, func(t *testing.T, report map[string]any) {
			health := arrayField(t, report, "source_health")[0].(map[string]any)
			health["last_error_class"] = "because"
		}},
		{"decision missing required field", false, func(t *testing.T, report map[string]any) {
			delete(decisionField(t, report, 0), "n")
		}},
		{"step missing prepared request key", false, func(t *testing.T, report map[string]any) {
			delete(reportStep(t, report, 0), "prepared_request")
		}},
		{"typed missing error class", false, func(t *testing.T, report map[string]any) {
			delete(typedField(t, report, 5), "error_class")
		}},
		{"passed run with provider-inconclusive outcome", true, func(t *testing.T, report map[string]any) {
			reportStep(t, report, 0)["outcome"] = "provider_inconclusive"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := cloneJSONObject(t, reportExample(t))
			test.mutate(t, report)
			if err := schema.validate(report); err == nil && !test.semanticOnly {
				t.Fatal("schema accepted inconsistent report evidence")
			}
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal mutated report: %v", err)
			}
			if _, err := ParseReport(data); err == nil {
				t.Fatal("ParseReport accepted inconsistent report evidence")
			}
		})
	}
}

func reportStep(t *testing.T, report map[string]any, index int) map[string]any {
	t.Helper()
	steps := arrayField(t, report, "steps")
	if index >= len(steps) {
		t.Fatalf("step %d is missing", index)
	}
	step, ok := steps[index].(map[string]any)
	if !ok {
		t.Fatalf("step %d is not an object", index)
	}
	return step
}

func decisionField(t *testing.T, report map[string]any, index int) map[string]any {
	t.Helper()
	decision, ok := reportStep(t, report, index)["decision"].(map[string]any)
	if !ok {
		t.Fatalf("step %d has no decision", index)
	}
	return decision
}

func referenceField(t *testing.T, report map[string]any, index int) map[string]any {
	t.Helper()
	reference, ok := reportStep(t, report, index)["reference"].(map[string]any)
	if !ok {
		t.Fatalf("step %d has no reference", index)
	}
	return reference
}

func typedField(t *testing.T, report map[string]any, index int) map[string]any {
	t.Helper()
	typed, ok := reportStep(t, report, index)["typed"].(map[string]any)
	if !ok {
		t.Fatalf("step %d has no typed result", index)
	}
	return typed
}

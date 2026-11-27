package livecanary

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestSchemaAndParserAgreeOnAdapterProvenance(t *testing.T) {
	const whitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"
	for _, test := range []struct {
		name       string
		value      any
		present    bool
		readable   bool
		executable bool
	}{
		{name: "missing", readable: true},
		{name: "null", present: true, readable: true},
		{name: "empty", present: true, value: "", readable: true},
		{name: "whitespace", present: true, value: whitespace, readable: true},
		{name: "geth", present: true, value: "geth", readable: true},
		{name: "normalized geth", present: true, value: whitespace + "GeTh" + whitespace, readable: true},
		{name: "native", present: true, value: "native", readable: true, executable: true},
		{name: "normalized native", present: true, value: whitespace + "NaTiVe" + whitespace, readable: true, executable: true},
		{name: "unicode lowercase native", present: true, value: "NAT\u0130VE", readable: true, executable: true},
		{name: "unknown", present: true, value: "other"},
		{name: "internal whitespace", present: true, value: "nat ive"},
		{name: "zero width space", present: true, value: "\u200bnative"},
		{name: "number", present: true, value: 1},
		{name: "boolean", present: true, value: true},
		{name: "array", present: true, value: []any{}},
		{name: "object", present: true, value: map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := manifestExample(t)
			delete(document, "http_adapter")
			if test.present {
				document["http_adapter"] = test.value
			}
			data, err := json.MarshalIndent(document, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(data)
			digest := ManifestSHA256(data)
			decoded, err := decodeJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			schemaErr := loadManifestSchema(t).validate(decoded)
			manifest, parseErr := ParseManifest(data)
			if (schemaErr == nil) != test.readable || (parseErr == nil) != test.readable {
				t.Fatalf("readable=%t: schema=%v, parser=%v", test.readable, schemaErr, parseErr)
			}
			if parseErr == nil {
				wantAdapter, _ := test.value.(string)
				if manifest.HTTPAdapter != wantAdapter {
					t.Fatalf("parser relabeled provenance %q as %q", wantAdapter, manifest.HTTPAdapter)
				}
				if err := manifest.CheckExecution(); (err == nil) != test.executable {
					t.Fatalf("executable=%t: %v", test.executable, err)
				}
			}
			if !bytes.Equal(data, original) || ManifestSHA256(data) != digest {
				t.Fatal("reading provenance changed evidence bytes or digest")
			}
		})
	}
}

func manifestExample(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "..", "docs", "design", "live-canary", "manifest.example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest example: %v", err)
	}
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatalf("decode manifest example: %v", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		t.Fatal("manifest example is not an object")
	}
	return object
}

func cloneJSONObject(t *testing.T, object map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal clone: %v", err)
	}
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatalf("decode clone: %v", err)
	}
	return decoded.(map[string]any)
}

func objectField(t *testing.T, object map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := object[name].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", name)
	}
	return value
}

func arrayField(t *testing.T, object map[string]any, name string) []any {
	t.Helper()
	value, ok := object[name].([]any)
	if !ok {
		t.Fatalf("%s is not an array", name)
	}
	return value
}

func TestManifestSchemaRefsResolve(t *testing.T) {
	schema := loadManifestSchema(t)
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

func TestManifestSchemaAcceptsGeneratedManifest(t *testing.T) {
	schema := loadManifestSchema(t)
	example := manifestExample(t)
	if err := schema.validate(example); err != nil {
		t.Fatalf("schema rejected manifest.example.json: %v", err)
	}
	exampleBytes, err := json.Marshal(example)
	if err != nil {
		t.Fatalf("marshal example: %v", err)
	}
	if _, err := ParseManifest(exampleBytes); err != nil {
		t.Fatalf("ParseManifest rejected the example: %v", err)
	}

	generated, err := buildFixture(t).Manifest.Marshal()
	if err != nil {
		t.Fatalf("marshal generated manifest: %v", err)
	}
	decoded, err := decodeJSON(generated)
	if err != nil {
		t.Fatalf("decode generated manifest: %v", err)
	}
	if err := schema.validate(decoded); err != nil {
		t.Fatalf("schema rejected a generated manifest: %v", err)
	}
}

func TestSerializedNativeManifestSatisfiesPublishedSchema(t *testing.T) {
	manifest := buildFixture(t).Manifest
	manifest.HTTPAdapter = HTTPAdapterNative
	data, err := manifest.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := decodeJSON(data)
	if err != nil {
		t.Fatalf("decode generated manifest: %v", err)
	}
	if err := loadManifestSchema(t).validate(decoded); err != nil {
		t.Fatalf("published schema rejects a serialized native manifest: %v", err)
	}
}

func TestManifestSchemaRequiresEveryTopLevelField(t *testing.T) {
	schema := loadManifestSchema(t)
	required, ok := schema.root["required"].([]any)
	if !ok {
		t.Fatal("schema has no required list")
	}
	example := manifestExample(t)
	for _, raw := range required {
		name := raw.(string)
		t.Run(name, func(t *testing.T) {
			clone := cloneJSONObject(t, example)
			delete(clone, name)
			if err := schema.validate(clone); err == nil {
				t.Fatalf("schema accepted a manifest without %q", name)
			}
			data, err := json.Marshal(clone)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, err := ParseManifest(data); err == nil {
				t.Fatalf("ParseManifest accepted a manifest without %q", name)
			}
		})
	}
}

func TestManifestSchemaRejectsInconsistentProvenance(t *testing.T) {
	schema := loadManifestSchema(t)
	tests := []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{"unknown top-level field", func(_ *testing.T, manifest map[string]any) {
			manifest["ai_filled"] = true
		}},
		{"unknown nested field", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "registry")["ai_note"] = "filled by hand"
		}},
		{"local run with CI identity", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "environment")["kind"] = "local"
		}},
		{"unavailable registry with selection", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "registry")["status"] = "unavailable"
		}},
		{"non-chainlist registry url", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "registry")["url"] = "https://attacker.example/registry.json"
		}},
		{"zero generated at", func(_ *testing.T, manifest map[string]any) {
			manifest["generated_at"] = "0001-01-01T00:00:00Z"
		}},
		{"zero fetched at", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "registry")["fetched_at"] = "0001-01-01T00:00:00Z"
		}},
		{"missing raw hash", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "registry")["raw_sha256"] = nil
		}},
		{"artifact without hash", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "registry")["artifact_sha256"] = nil
		}},
		{"bad filter reason", func(t *testing.T, manifest map[string]any) {
			filtered := arrayField(t, objectField(t, manifest, "selection"), "filtered")
			filtered[0].(map[string]any)["reason"] = "because"
		}},
		{"bad plan path", func(t *testing.T, manifest map[string]any) {
			steps := arrayField(t, objectField(t, manifest, "plan"), "steps")
			steps[0].(map[string]any)["path"] = "shell"
		}},
		{"wrong plan hash", func(t *testing.T, manifest map[string]any) {
			objectField(t, manifest, "plan")["sha256"] =
				"0000000000000000000000000000000000000000000000000000000000000000"
		}},
		{"extra plan step", func(t *testing.T, manifest map[string]any) {
			plan := objectField(t, manifest, "plan")
			steps := arrayField(t, plan, "steps")
			plan["steps"] = append(steps, cloneJSONObject(t, steps[0].(map[string]any)))
		}},
		{"http selected member", func(t *testing.T, manifest map[string]any) {
			selection := objectField(t, manifest, "selection")
			selected := arrayField(t, selection, "selected")
			selected[0].(map[string]any)["url"] = "http://rpc-a.example"
		}},
		{"missing timeout", func(t *testing.T, manifest map[string]any) {
			delete(objectField(t, manifest, "timeouts"), "run_ms")
		}},
		{"wrong chain id", func(_ *testing.T, manifest map[string]any) {
			manifest["chain_id"] = 5
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneJSONObject(t, manifestExample(t))
			test.mutate(t, manifest)
			if err := schema.validate(manifest); err == nil {
				t.Fatal("schema accepted inconsistent provenance")
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("marshal mutated manifest: %v", err)
			}
			if _, err := ParseManifest(data); err == nil {
				t.Fatal("ParseManifest accepted inconsistent provenance")
			}
		})
	}
}

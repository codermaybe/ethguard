package executionapi

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestManifestStrictContract(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.ResultMethods) != 31 || len(manifest.RequestRoots) != 6 || manifest.SHA256 == "" {
		t.Fatal("missing migrated contract")
	}
	if len(manifest.ValueValidationResults) != 1 || manifest.ValueValidationResults[0] != "eth_createAccessList" {
		t.Fatal("value validation pilot must select only eth_createAccessList")
	}
	for name, edit := range map[string]func(map[string]any){
		"unknown":          func(m map[string]any) { m["notAContractField"] = true },
		"version":          func(m map[string]any) { m["formatVersion"] = 2 },
		"duplicate-result": func(m map[string]any) { m["resultMethods"] = []string{"eth_call", "eth_call"} },
		"duplicate-validator": func(m map[string]any) {
			m["valueValidationResults"] = []string{"eth_createAccessList", "eth_createAccessList"}
		},
		"unselected-validator": func(m map[string]any) { m["valueValidationResults"] = []string{"eth_missing"} },
		"missing-reason":       func(m map[string]any) { m["defaultExclusionReason"] = "" },
		"alias-cycle":          func(m map[string]any) { m["aliases"] = map[string]string{"Alpha": "Beta", "Beta": "Alpha"} },
		"bad-name":             func(m map[string]any) { m["titleNames"] = map[string]string{"title": "private"} },
		"order-duplicate":      func(m map[string]any) { m["fieldOrder"] = map[string][]string{"Log": {"data", "data"}} },
		"subtree-without-identity": func(m map[string]any) {
			m["requestRoots"] = []any{map[string]any{"source": map[string]any{"method": "eth_subscribe", "part": "request", "parameter": 1, "schemaPointer": "/oneOf/2"}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			edit(value)
			changed, _ := json.Marshal(value)
			if _, err := ParseManifest(changed); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	if _, err := ParseManifest([]byte(strings.Replace(string(raw), `"formatVersion": 1`, `"formatVersion": 1, "formatVersion": 1`, 1))); err == nil {
		t.Fatal("duplicate JSON key accepted")
	}
	if _, err := ParseManifest(append(raw, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestSchemaReferenceEscapesAndSelectedIdentity(t *testing.T) {
	raw := json.RawMessage(`{"properties":{"a/b~c":{"type":"boolean"}},"oneOf":[{"type":"null"},{"type":"string"}]}`)
	selected, err := SchemaAtPointer(raw, "/properties/a~1b~0c")
	if err != nil || string(selected) != `{"type":"boolean"}` {
		t.Fatalf("escaped pointer: %s %v", selected, err)
	}
	for _, bad := range []string{"properties", "/properties/a~2b", "/oneOf/01", "/oneOf/-1", "/oneOf/2", "/oneOf/0/type"} {
		if _, err := SchemaAtPointer(raw, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	index := 0
	ref := SchemaRef{Method: "eth_test", Part: "request", Parameter: &index, Pointer: "/properties/a~1b~0c"}
	if ref.Path() != "eth_test.params[0]/properties/a~1b~0c" {
		t.Fatal(ref.Path())
	}
	bundle := Bundle{Requests: map[string][]Parameter{"eth_test": {{Schema: raw}}}}
	if _, err := SelectSchema(bundle, SchemaSelection{Source: ref, ExpectedSHA256: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("identity drift accepted")
	}
	for _, bad := range []SchemaRef{{Method: "eth_test", Part: "request"}, {Method: "eth_test", Part: "result", Parameter: &index}, {Method: "eth.test", Part: "result"}} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

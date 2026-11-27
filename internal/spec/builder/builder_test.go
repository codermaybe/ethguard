package builder

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSelectsSortedOfficialResultSchemas(t *testing.T) {
	openRPC := []byte(`{"methods":[{"name":"eth_z","result":{"schema":{"type":"string"}}},{"name":"eth_a","result":{"schema":{"type":"array","items":{"type":"number"}}}}]}`)
	bundle, err := Build(openRPC, "v2", []string{"eth_z", "eth_a"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(bundle, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != "v2" || len(decoded.Results) != 2 {
		t.Fatalf("bundle = %s", bundle)
	}
	var schema map[string]any
	if err := json.Unmarshal(decoded.Results["eth_a"], &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "array" {
		t.Fatalf("eth_a schema = %s", decoded.Results["eth_a"])
	}
}

func TestBuildRejectsMissingSchemaAndDuplicateMethod(t *testing.T) {
	openRPC := []byte(`{"methods":[{"name":"eth_x","result":{"schema":{"type":"string"}}}]}`)
	if _, err := Build(openRPC, "v1", []string{"eth_missing"}); err == nil {
		t.Fatal("missing method accepted")
	}
	if _, err := Build(openRPC, "v1", []string{"eth_x", "eth_x"}); err == nil {
		t.Fatal("duplicate method accepted")
	}
}

func TestMethodsFromBundleIsSorted(t *testing.T) {
	methods, err := MethodsFromBundle([]byte(`{"results":{"z":{},"a":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "a,z" {
		t.Fatalf("methods = %#v", methods)
	}
}

func TestMethodsFromListIgnoresCommentsAndRejectsDuplicates(t *testing.T) {
	methods, err := MethodsFromList([]byte("# policy\nz\n\na\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "a,z" {
		t.Fatalf("methods = %#v", methods)
	}
	if _, err := MethodsFromList([]byte("eth_x\neth_x\n")); err == nil {
		t.Fatal("duplicate method accepted")
	}
}

func TestBuildRequestsPreservesDescriptorsAndOrdering(t *testing.T) {
	const source = `{"methods":[{"name":"debug_trace","params":[]},{"name":"eth_z","params":[{"name":"query","required":false,"summary":"keep descriptor metadata","schema":{"type":"object","properties":{"value":{"type":"number","minimum":0.30000000000000001}}}},{"name":"block","required":true,"schema":{"type":"string"}}]},{"name":"net_version","params":[]},{"name":"eth_a","params":[]}]}`
	raw, err := BuildRequests([]byte(source), "pin")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version  string
		Requests map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "pin" || len(got.Requests) != 3 || got.Requests["debug_trace"] != nil {
		t.Fatalf("inventory = %s", raw)
	}
	var document struct {
		Methods []struct {
			Name   string
			Params json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(source), &document); err != nil {
		t.Fatal(err)
	}
	var want, actual bytes.Buffer
	if err := json.Compact(&want, document.Methods[1].Params); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&actual, got.Requests["eth_z"]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.Bytes(), actual.Bytes()) {
		t.Fatalf("descriptor changed: %s -> %s", want.Bytes(), actual.Bytes())
	}
	if strings.Index(string(raw), `"eth_a"`) >= strings.Index(string(raw), `"eth_z"`) {
		t.Fatalf("unstable inventory order: %s", raw)
	}
}

func TestBuildRequestsRejectsAmbiguousOrMissingSource(t *testing.T) {
	for _, tc := range []struct{ name, source, version, want string }{
		{"version", `{}`, "", "version is required"},
		{"inventory", `{"methods":[{"name":"debug_x","params":[]}]}`, "pin", "no Ethereum request schemas"},
		{"missing", `{"methods":[{"name":"eth_x"}]}`, "pin", `method "eth_x" has no params descriptor`},
		{"null", `{"methods":[{"name":"eth_x","params":null}]}`, "pin", `method "eth_x" has no params descriptor`},
		{"duplicate", `{"methods":[{"name":"eth_x","params":[]},{"name":"eth_x","params":[]}]}`, "pin", `duplicate method "eth_x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildRequests([]byte(tc.source), tc.version)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

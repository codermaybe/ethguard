package builder

import (
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

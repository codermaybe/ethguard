package builder

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The layout migration must retain the two former source snapshots exactly.
// Reconstructing their historical envelopes checks their independently recorded
// digests without keeping a second active copy of any official schema.
func TestUnifiedLayoutPreservesHistoricalSourceSnapshots(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		// Reconstruct the historical 24-result snapshot before checking the
		// immutable layout migration record. The Header extension has its own
		// independent transition record and source checks.
		if name == "methods.json" {
			var bundle struct {
				Version string                     `json:"version"`
				Results map[string]json.RawMessage `json:"results"`
			}
			if err := json.Unmarshal(raw, &bundle); err != nil {
				t.Fatal(err)
			}
			delete(bundle.Results, "eth_getHeaderByHash")
			delete(bundle.Results, "eth_getHeaderByNumber")
			var buf bytes.Buffer
			encoder := json.NewEncoder(&buf)
			encoder.SetIndent("", " ")
			if err := encoder.Encode(bundle); err != nil {
				t.Fatal(err)
			}
			raw = buf.Bytes()
		}
		return raw
	}
	var migration struct {
		Kind                      string
		BeforeVersion             string
		AfterVersion              string
		BeforeBundles             map[string]string
		AfterBundles              map[string]string
		AfterVectorManifestSHA256 string
		VectorRelocation          struct {
			From, To        string
			SHA256Unchanged map[string]string
		}
		UnchangedErrataSHA256 map[string]string
	}
	if err := json.Unmarshal(read("migrations/981aa66d79364be8322a9c358aaf0a571cca0873-unified-layout.json"), &migration); err != nil {
		t.Fatal(err)
	}
	if migration.Kind != "reference-layout-unification" || migration.BeforeVersion != migration.AfterVersion {
		t.Fatal("layout unification changed the upstream identity")
	}
	check := func(name string, raw []byte, want string) {
		t.Helper()
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); want == "" || got != want {
			t.Fatalf("%s digest = %s, want %s", name, got, want)
		}
	}
	for name, want := range migration.AfterBundles {
		check(name, read(name), want)
	}
	check("requests unchanged", read("requests.json"), migration.BeforeBundles["requests.json"])
	check("unified vector inventory", read("vectors/SOURCE.json"), migration.AfterVectorManifestSHA256)
	for name, want := range migration.UnchangedErrataSHA256 {
		check(name, read("errata/"+name), want)
	}
	if len(migration.UnchangedErrataSHA256) != 3 || len(migration.VectorRelocation.SHA256Unchanged) != 95 || migration.VectorRelocation.From != "simulatev1/vectors/" || migration.VectorRelocation.To != "vectors/eth_simulateV1/" {
		t.Fatal("incomplete errata or vector relocation inventory")
	}
	for name, want := range migration.VectorRelocation.SHA256Unchanged {
		check(name, read(migration.VectorRelocation.To+name), want)
	}

	var methods struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(read("methods.json"), &methods); err != nil {
		t.Fatal(err)
	}
	list, err := MethodsFromList(read("methods.list"))
	if err != nil {
		t.Fatal(err)
	}
	if len(methods.Results) != 24 || len(list) != len(methods.Results)+2 || methods.Version != migration.AfterVersion {
		t.Fatal("unified result policy or source identity differs")
	}
	for _, name := range list {
		if name == "eth_getHeaderByHash" || name == "eth_getHeaderByNumber" {
			continue
		}
		if len(methods.Results[name]) == 0 {
			t.Fatalf("policy method %s has no result schema", name)
		}
	}
	simulateResult := methods.Results["eth_simulateV1"]
	delete(methods.Results, "eth_simulateV1")
	encode := func(value any) []byte {
		t.Helper()
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetIndent("", " ")
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	check("historical 23-result bundle", encode(methods), migration.BeforeBundles["methods.json"])

	// The retired single-method emitter stored name/required/schema descriptors;
	// the shared inventory retains the full official parameter metadata instead.
	type historicalParameter struct {
		Name     string          `json:"name"`
		Required bool            `json:"required,omitempty"`
		Schema   json.RawMessage `json:"schema"`
	}
	var requests struct {
		Version  string
		Requests map[string][]historicalParameter
	}
	if err := json.Unmarshal(read("requests.json"), &requests); err != nil {
		t.Fatal(err)
	}
	if len(requests.Requests) != 49 || requests.Version != methods.Version {
		t.Fatal("unified request inventory differs")
	}
	historical := struct {
		Version string                `json:"version"`
		Method  string                `json:"method"`
		Params  []historicalParameter `json:"params"`
		Result  json.RawMessage       `json:"result"`
	}{methods.Version, "eth_simulateV1", requests.Requests["eth_simulateV1"], simulateResult}
	check("historical single-method bundle", encode(historical), migration.BeforeBundles["simulatev1/simulate.json"])
}

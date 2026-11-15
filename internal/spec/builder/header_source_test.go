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

func TestHeaderCoverageExtensionPreservesOfficialSources(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var record struct {
		Kind, Version, Commit, DereferencedOpenRPCSHA256                 string
		BeforeMethodsSHA256, AfterMethodsSHA256, UnchangedRequestsSHA256 string
		BeforeResultCount, AfterResultCount                              int
		AddedResultSchemasCompactSHA256, UnchangedErrataSHA256           map[string]string
	}
	if err := json.Unmarshal(read("migrations/981aa66d79364be8322a9c358aaf0a571cca0873-header-models.json"), &record); err != nil {
		t.Fatal(err)
	}
	if record.Kind != "official-result-model-coverage-extension" || record.Version != "981aa66d79364be8322a9c358aaf0a571cca0873" || record.Commit != record.Version || record.DereferencedOpenRPCSHA256 != "80efa2c6ab609d9ec48fa6d720650d1c3903065c887e03c600970ca97c2f09a5" || record.BeforeResultCount != 24 || record.AfterResultCount != 26 {
		t.Fatal("incorrect coverage transition identity")
	}
	check := func(name string, raw []byte, want string) {
		t.Helper()
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
			t.Fatalf("%s digest=%s want=%s", name, got, want)
		}
	}
	check("methods", read("methods.json"), record.AfterMethodsSHA256)
	check("requests", read("requests.json"), record.UnchangedRequestsSHA256)
	if len(record.UnchangedErrataSHA256) != 3 || len(record.AddedResultSchemasCompactSHA256) != 2 {
		t.Fatal("incomplete coverage transition")
	}
	for name, want := range record.UnchangedErrataSHA256 {
		check(name, read("errata/"+name), want)
	}
	var methods struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(read("methods.json"), &methods); err != nil {
		t.Fatal(err)
	}
	if len(methods.Results) != 26 {
		t.Fatal("expected exactly two added official results")
	}
	for _, name := range []string{"eth_getHeaderByHash", "eth_getHeaderByNumber"} {
		var compact bytes.Buffer
		if err := json.Compact(&compact, methods.Results[name]); err != nil {
			t.Fatal(err)
		}
		// Digest extracted directly from the fixed official OpenRPC, independently
		// of this repository's generator, Header type, or transition manifest.
		const official = "89e280d12c6d35f9490b5a01fdca58725928dd04c5aadbd06b66cdc976868ae5"
		if record.AddedResultSchemasCompactSHA256[name] != official {
			t.Fatal("header source digest changed")
		}
		check(name, compact.Bytes(), official)
		delete(methods.Results, name)
	}
	var reconstructed bytes.Buffer
	encoder := json.NewEncoder(&reconstructed)
	encoder.SetIndent("", " ")
	if err := encoder.Encode(methods); err != nil {
		t.Fatal(err)
	}
	check("historical unified source", reconstructed.Bytes(), record.BeforeMethodsSHA256)
	if record.BeforeMethodsSHA256 != "e4b0aa59e595f9fb030e8386b6e37b7f4ea33ea33acb043a88ac2e0081fae336" {
		t.Fatal("historical migration record drift")
	}
}

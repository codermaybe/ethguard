package specsync

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec/builder"
)

func testWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := writeJSON(path, value); err != nil {
		t.Fatal(err)
	}
}
func fixtureWorkspace(t *testing.T) (string, Inputs) {
	t.Helper()
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	spec := filepath.Join(workspace, "internal/spec")
	commit := strings.Repeat("a", 40)
	openrpc := []byte(`{"methods":[{"name":"eth_fixture","params":[{"name":"value","required":true,"schema":{"type":"string"}}],"result":{"schema":{"type":"boolean"}}}]}`)
	manifest := executionapi.Manifest{FormatVersion: 1, ResultMethods: []string{"eth_fixture"}, RequestInventoryPrefixes: []string{"eth_"}, DefaultExclusionReason: "fixture boundary"}
	testWriteJSON(t, filepath.Join(workspace, "internal/executionapi/manifest.json"), manifest)
	artifacts, err := builder.BuildSelected(openrpc, commit, manifest)
	if err != nil {
		t.Fatal(err)
	}
	vector := []byte(">> {\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_fixture\",\"params\":[\"x\"]}\n<< {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":true}\n")
	vectors := VectorSource{Repository: "fixture://synthetic", Version: commit, Commit: commit, UpstreamVectorDirectory: "tests", Vectors: map[string]string{"eth_fixture/check.io": hash(vector)}}
	vectorRaw, err := encodeJSON(vectors)
	if err != nil {
		t.Fatal(err)
	}
	source := executionapi.SourceMetadata{Repository: vectors.Repository, Version: commit, Commit: commit, Category: "fixture", DereferencedOpenRPCSHA256: hash(openrpc), Bundles: map[string]string{"methods.json": hash(artifacts.Methods), "requests.json": hash(artifacts.Requests), "inventory.json": hash(artifacts.Inventory)}, VectorManifestSHA256: hash(vectorRaw), Build: executionapi.SourceBuild{Tool: "synthetic-fixture", Commit: commit, Arguments: []string{"fixture"}, GoVersion: "fixture"}}
	for name, raw := range map[string][]byte{"VERSION": []byte(commit + "\n"), "methods.json": artifacts.Methods, "requests.json": artifacts.Requests, "inventory.json": artifacts.Inventory, "vectors/SOURCE.json": vectorRaw, "vectors/eth_fixture/check.io": vector, "errata/README.md": []byte("No fixture overlays.")} {
		if err := writeFile(filepath.Join(spec, name), raw); err != nil {
			t.Fatal(err)
		}
	}
	testWriteJSON(t, filepath.Join(spec, "SOURCE.json"), source)
	input := Inputs{OpenRPC: filepath.Join(dir, "openrpc.json"), VectorsDir: filepath.Join(spec, "vectors"), Source: filepath.Join(dir, "source.json")}
	if err := writeFile(input.OpenRPC, openrpc); err != nil {
		t.Fatal(err)
	}
	testWriteJSON(t, input.Source, MaterialSource{source, vectors})
	return workspace, input
}
func TestInputModesDoNotFallback(t *testing.T) {
	for _, input := range []Inputs{{}, {Commit: "main"}, {Commit: strings.Repeat("a", 40), UpstreamDir: "local"}, {OpenRPC: "file"}, {UpstreamDir: "local", OpenRPC: "file", VectorsDir: "dir", Source: "meta"}} {
		if _, err := input.Mode(); err == nil {
			t.Fatalf("accepted ambiguous mode: %+v", input)
		}
	}
	for _, input := range []Inputs{{Commit: strings.Repeat("a", 40)}, {UpstreamDir: "local"}, {OpenRPC: "file", VectorsDir: "dir", Source: "meta"}} {
		if _, err := input.Mode(); err != nil {
			t.Fatal(err)
		}
	}
	for _, setting := range []string{"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOVCS=*:off"} {
		found := false
		for _, value := range offlineEnvironment() {
			found = found || value == setting
		}
		if !found {
			t.Fatalf("missing offline boundary %s", setting)
		}
	}
}
func TestOfflineFixtureNoopAndRequestResultDiff(t *testing.T) {
	workspace, input := fixtureWorkspace(t)
	before, err := treeDigests(workspace)
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(t.TempDir(), "candidate")
	report, err := Run(context.Background(), Options{Inputs: input, Workspace: workspace, Out: first, PrepareOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Publishable || len(report.RawDiff.Results) != 0 || len(report.RawDiff.Requests) != 0 {
		t.Fatalf("bad prepare-only report: %+v", report)
	}
	second := filepath.Join(t.TempDir(), "candidate")
	if _, err := Run(context.Background(), Options{Inputs: input, Workspace: workspace, Out: second, PrepareOnly: true}); err != nil {
		t.Fatal(err)
	}
	a, err := treeDigests(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := treeDigests(second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same offline materials did not produce deterministic diagnostics")
	}
	raw, err := os.ReadFile(input.OpenRPC)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"name":"value","required":true`), []byte(`"name":"renamed","required":false`), 1)
	raw = bytes.Replace(raw, []byte(`"type":"boolean"`), []byte(`"type":"integer","minimum":0`), 1)
	if err := os.WriteFile(input.OpenRPC, raw, 0644); err != nil {
		t.Fatal(err)
	}
	var metadata MaterialSource
	if err := readJSON(input.Source, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.Source.DereferencedOpenRPCSHA256 = hash(raw)
	testWriteJSON(t, input.Source, metadata)
	changed, err := Run(context.Background(), Options{Inputs: input, Workspace: workspace, Out: filepath.Join(t.TempDir(), "candidate"), PrepareOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.RawDiff.Results) != 1 || len(changed.RawDiff.Requests) != 1 {
		t.Fatalf("missing one half of upgrade: %+v", changed.RawDiff)
	}
	after, err := treeDigests(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("candidate extraction polluted active inputs")
	}
}
func TestOfflineMaterialsRejectMissingChangedAndForgedInputs(t *testing.T) {
	for _, kind := range []string{"openrpc", "missing-vector", "extra-vector", "forged-official"} {
		t.Run(kind, func(t *testing.T) {
			workspace, input := fixtureWorkspace(t)
			switch kind {
			case "openrpc":
				if err := os.WriteFile(input.OpenRPC, []byte(`{}`), 0644); err != nil {
					t.Fatal(err)
				}
			case "missing-vector":
				if err := os.Remove(filepath.Join(input.VectorsDir, "eth_fixture/check.io")); err != nil {
					t.Fatal(err)
				}
			case "extra-vector":
				if err := writeFile(filepath.Join(input.VectorsDir, "eth_fixture/extra.io"), []byte("extra")); err != nil {
					t.Fatal(err)
				}
			case "forged-official":
				var source MaterialSource
				if err := readJSON(input.Source, &source); err != nil {
					t.Fatal(err)
				}
				source.Source.Category = "official"
				source.Source.Repository = Repository
				source.Vectors.Repository = Repository
				testWriteJSON(t, input.Source, source)
			}
			out := filepath.Join(t.TempDir(), "candidate")
			report, err := Run(context.Background(), Options{Inputs: input, Workspace: workspace, Out: out, PrepareOnly: true})
			if err == nil || report.Ready || report.Publishable {
				t.Fatalf("invalid material accepted: %v %+v", err, report)
			}
			if _, err := os.Stat(filepath.Join(out, "report.json")); err != nil {
				t.Fatal("failure lost report", err)
			}
		})
	}
}
func TestReadFromVerifiesDescriptorIdentityAndFixtureBoundary(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	root := filepath.Join(workspace, "internal/spec")
	original, err := executionapi.ReadOriginalFrom(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if original.Source.Category != "fixture" {
		t.Fatal("fixture provenance lost")
	}
	path := filepath.Join(root, "requests.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"required": true`), []byte(`"required": false`), 1)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	var source executionapi.SourceMetadata
	if err := readJSON(filepath.Join(root, "SOURCE.json"), &source); err != nil {
		t.Fatal(err)
	}
	source.Bundles["requests.json"] = hash(raw)
	testWriteJSON(t, filepath.Join(root, "SOURCE.json"), source)
	if _, err := executionapi.ReadOriginalFrom(os.DirFS(root)); err == nil || !strings.Contains(err.Error(), "descriptors") {
		t.Fatalf("rehashed descriptor drift bypassed inventory: %v", err)
	}
}
func TestSnapshotIncludesUncommittedFilesAndIsolatesGit(t *testing.T) {
	source := t.TempDir()
	dest := filepath.Join(t.TempDir(), "snapshot")
	for name, value := range map[string]string{"tracked.go": "changed", "new.go": "uncommitted", ".git/HEAD": "private git state"} {
		if err := writeFile(filepath.Join(source, name), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := copyTree(source, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 || inventory["new.go"] == "" {
		t.Fatalf("snapshot omitted real working input: %+v", inventory)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Fatal("snapshot copied active git metadata")
	}
}
func TestGeneratedComparisonIncludesPublicSignatures(t *testing.T) {
	a, err := publicSignatures([]byte("package p\ntype Value string\nfunc Parse(v string) (Value,error) {return Value(v),nil}\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := publicSignatures([]byte("package p\ntype Value []byte\nfunc Parse(v []byte) (Value,error) {return Value(v),nil}\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a, b) || !strings.Contains(string(raw), "Parse") {
		t.Fatal("public type/function change omitted")
	}
}

// This test executes a real -trimpath release binary, the real compiler and its
// emitted Go in a snapshot with a different fixture VERSION. It catches missing
// GOROOT in distributed binaries, active-pin reuse and silent input downloads.
func TestOfflineFixtureGeneratesAndCompilesIsolatedVersion(t *testing.T) {
	workspace, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var results struct{ Results map[string]json.RawMessage }
	var requests struct{ Requests map[string]json.RawMessage }
	resultBytes, err := os.ReadFile(filepath.Join(workspace, "internal/spec/methods.json"))
	if err != nil {
		t.Fatal(err)
	}
	requestBytes, err := os.ReadFile(filepath.Join(workspace, "internal/spec/requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resultBytes, &results); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(requestBytes, &requests); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(requests.Requests))
	for name := range requests.Requests {
		names = append(names, name)
	}
	sort.Strings(names)
	type method struct {
		Name   string          `json:"name"`
		Params json.RawMessage `json:"params"`
		Result struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"result"`
	}
	document := struct {
		Methods []method `json:"methods"`
	}{Methods: []method{}}
	for _, name := range names {
		m := method{Name: name, Params: requests.Requests[name]}
		m.Result.Schema = results.Results[name]
		if len(m.Result.Schema) == 0 {
			m.Result.Schema = json.RawMessage(`{"type":"boolean"}`)
		}
		if name == "eth_chainId" {
			var schema map[string]json.RawMessage
			if err := json.Unmarshal(m.Result.Schema, &schema); err != nil {
				t.Fatal(err)
			}
			schema["description"] = json.RawMessage(`"synthetic upgrade coverage"`)
			raw, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			m.Result.Schema = raw
		}
		if name == "eth_getBalance" {
			var params []map[string]json.RawMessage
			if err := json.Unmarshal(m.Params, &params); err != nil {
				t.Fatal(err)
			}
			params[0]["name"] = json.RawMessage(`"syntheticAccount"`)
			raw, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			m.Params = raw
		}
		document.Methods = append(document.Methods, m)
	}
	dir := t.TempDir()
	openrpc, err := encodeJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	input := Inputs{OpenRPC: filepath.Join(dir, "openrpc.json"), VectorsDir: filepath.Join(workspace, "internal/spec/vectors"), Source: filepath.Join(dir, "material.json")}
	if err := writeFile(input.OpenRPC, openrpc); err != nil {
		t.Fatal(err)
	}
	var source executionapi.SourceMetadata
	var vectors VectorSource
	if err := readJSON(filepath.Join(workspace, "internal/spec/SOURCE.json"), &source); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(input.VectorsDir, "SOURCE.json"), &vectors); err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("b", 40)
	source.Repository = "fixture://synthetic-upgrade"
	source.Commit = commit
	source.Version = commit
	source.Category = "fixture"
	source.DereferencedOpenRPCSHA256 = hash(openrpc)
	source.Build = executionapi.SourceBuild{Tool: "synthetic-fixture", Commit: commit, Arguments: []string{"fixture"}, GoVersion: "fixture"}
	vectors.Repository = source.Repository
	vectors.Version = commit
	vectors.Commit = commit
	testWriteJSON(t, input.Source, MaterialSource{source, vectors})
	generatedPath := filepath.Join(workspace, "ethapi/types.gen.go")
	before, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "candidate")
	toolchain, err := selectGoToolchain(t.Context(), workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "specsync")
	if _, err := command(t.Context(), workspace, true, toolchain.Path, "build", "-trimpath", "-o", binary, "./cmd/specsync"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOROOT", "")
	t.Setenv("PATH", filepath.Dir(toolchain.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOTOOLCHAIN", "go1.999.0") // Must not launch or download this toolchain.
	if _, err := command(t.Context(), workspace, true, binary, "-openrpc", input.OpenRPC, "-vectors-dir", input.VectorsDir, "-source", input.Source, "-workspace", workspace, "-out", out); err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := readJSON(filepath.Join(out, "report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if report.Toolchain == nil || report.Toolchain.Version != toolchain.Version || report.Toolchain.Path != toolchain.Path {
		t.Fatalf("release binary failed to identify installed toolchain: %+v", report.Toolchain)
	}
	if !report.Ready || report.Publishable || report.Source.Category != "fixture" || report.Source.Commit != commit || report.SnapshotSHA256 == "" || report.Generated == nil {
		t.Fatalf("invalid fixture readiness: %+v", report)
	}
	if len(report.RawDiff.Results) == 0 || len(report.RawDiff.Requests) == 0 {
		t.Fatal("fixture omitted result/request upgrade")
	}
	if len(report.Validation) < 2 || report.Validation[0].Status != "passed" || report.Validation[1].Status != "passed" {
		t.Fatalf("compiler or build did not execute: %+v", report.Validation)
	}
	after, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("fixture mutated active generated models")
	}
	candidate, err := os.ReadFile(filepath.Join(out, "generated/types.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(candidate, []byte(commit)) {
		t.Fatal("candidate retained active source pin")
	}
}

func TestCandidateRejectsSymlinkAliasesIntoWorkspace(t *testing.T) {
	workspace, input := fixtureWorkspace(t)
	parent := t.TempDir()
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	before, err := treeDigests(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range []Options{
		{Inputs: input, Workspace: workspace, Out: filepath.Join(alias, "nested", "candidate"), PrepareOnly: true},
		{Inputs: input, Workspace: alias, Out: filepath.Join(workspace, "candidate"), PrepareOnly: true},
		{Inputs: input, Workspace: alias, Out: alias, PrepareOnly: true},
	} {
		if _, err := Run(t.Context(), options); err == nil || !strings.Contains(err.Error(), "outside the active workspace") {
			t.Fatalf("accepted output alias: %+v: %v", options, err)
		}
	}
	after, err := treeDigests(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected output alias modified workspace")
	}
	external := filepath.Join(t.TempDir(), "candidate")
	if _, err := Run(t.Context(), Options{Inputs: input, Workspace: alias, Out: external, PrepareOnly: true}); err != nil {
		t.Fatalf("legitimate aliased workspace rejected: %v", err)
	}
}

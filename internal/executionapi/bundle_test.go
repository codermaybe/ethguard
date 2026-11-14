package executionapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
)

func TestReadIncludesOriginalSimulateSchemaWithoutMutatingSources(t *testing.T) {
	before := append([]byte(nil), spec.MethodsJSON()...)
	bundle, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	var source struct{ Result json.RawMessage }
	if err := json.Unmarshal(spec.SimulateV1JSON(), &source); err != nil {
		t.Fatal(err)
	}
	if bundle.Version != spec.Version() || !bytes.Equal(bundle.Results["eth_simulateV1"], source.Result) {
		t.Fatal("schema assembly changed the official simulate definition")
	}
	delete(bundle.Results, "eth_chainId")
	again, err := Read()
	if err != nil || len(again.Results["eth_chainId"]) == 0 || !bytes.Equal(before, spec.MethodsJSON()) {
		t.Fatal("a consumer mutated the shared source")
	}
}

func TestLoadRejectsSourceSkewAndAmbiguity(t *testing.T) {
	const methods = `{"version":"pin","results":{"eth_chainId":{"type":"string"}}}`
	const simulate = `{"version":"pin","method":"eth_simulateV1","result":{"type":"array","items":{"type":"object"}}}`
	for _, tc := range []struct{ name, methods, simulate, want string }{
		{"method version", methods, simulate, "execution-apis methods version \"pin\" != pin \"other\""},
		{"simulate version", methods, `{"version":"old"}`, "execution-apis simulate version \"old\" != pin \"pin\""},
		{"missing simulate", methods, `{"version":"pin","method":"eth_simulateV1"}`, "execution-apis simulate bundle has no eth_simulateV1 result schema"},
		{"duplicate", `{"version":"pin","results":{"eth_simulateV1":{}}}`, simulate, "execution-apis duplicate result schema for eth_simulateV1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pin := "pin"
			if tc.name == "method version" {
				pin = "other"
			}
			_, err := load([]byte(tc.methods), []byte(tc.simulate), pin)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRequestInventorySharesTheOriginalCallAndSimulateSchemas(t *testing.T) {
	bundle, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw json.RawMessage) any {
		t.Helper()
		var value any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	call := bundle.Requests["eth_call"]
	if len(call) == 0 {
		t.Fatal("missing eth_call request schema")
	}
	for _, method := range []string{"eth_estimateGas", "eth_createAccessList"} {
		params := bundle.Requests[method]
		if len(params) == 0 || !reflect.DeepEqual(decode(call[0].Schema), decode(params[0].Schema)) {
			t.Fatalf("%s cannot share the generated CallMsg input", method)
		}
	}
	var source struct{ Params []Parameter }
	if err := json.Unmarshal(spec.SimulateV1JSON(), &source); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decode(mustJSON(t, source.Params)), decode(mustJSON(t, bundle.Requests["eth_simulateV1"]))) {
		t.Fatal("simulate request schema or descriptor differs between official inventories")
	}
	delete(bundle.Requests, "eth_call")
	again, err := Read()
	if err != nil || len(again.Requests["eth_call"]) == 0 {
		t.Fatalf("request source was mutated: %v", err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRequestsRejectVersionSkewAndIncompleteDescriptors(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"version", `{"version":"old"}`, `execution-apis requests version "old" != pin "pin"`},
		{"empty", `{"version":"pin","requests":{}}`, "execution-apis requests bundle is empty"},
		{"null params", `{"version":"pin","requests":{"eth_x":null}}`, "execution-apis eth_x: params must be an array"},
		{"name", `{"version":"pin","requests":{"eth_x":[{"schema":{}}]}}`, `execution-apis eth_x params[0]: missing or duplicate name ""`},
		{"duplicate", `{"version":"pin","requests":{"eth_x":[{"name":"x","schema":{}},{"name":"x","schema":{}}]}}`, `execution-apis eth_x params[1]: missing or duplicate name "x"`},
		{"schema", `{"version":"pin","requests":{"eth_x":[{"name":"x","schema":null}]}}`, "execution-apis eth_x params[0]: schema must be an object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRequests([]byte(tc.source), "pin")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestReferenceBundlesMatchPinnedSourceManifest(t *testing.T) {
	var source struct {
		Repository, Version, Commit, DereferencedOpenRPCSHA256 string
		Bundles                                                map[string]string
	}
	if err := json.Unmarshal(spec.SourceJSON(), &source); err != nil {
		t.Fatal(err)
	}
	if source.Repository != "https://github.com/ethereum/execution-apis" || source.Version != spec.Version() || source.Commit != "981aa66d79364be8322a9c358aaf0a571cca0873" || len(source.DereferencedOpenRPCSHA256) != 64 {
		t.Fatalf("unexpected source identity: %+v", source)
	}
	inputs := map[string][]byte{"methods.json": spec.MethodsJSON(), "requests.json": spec.RequestsJSON(), "simulatev1/simulate.json": spec.SimulateV1JSON()}
	if len(inputs) != len(source.Bundles) {
		t.Fatalf("source inventory = %+v", source.Bundles)
	}
	for name, raw := range inputs {
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != source.Bundles[name] {
			t.Fatalf("%s digest = %s, want %s; rebuild from the pinned official source and review the change", name, got, source.Bundles[name])
		}
	}
}

func TestSharedVectorInventoryMatchesPinnedSourceManifest(t *testing.T) {
	files := spec.VectorsFS()
	raw, err := fs.ReadFile(files, "vectors/SOURCE.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Repository, Version, Commit, UpstreamVectorDirectory string
		Vectors                                              map[string]string
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if source.Repository != "https://github.com/ethereum/execution-apis" || source.Version != spec.Version() || source.Commit != "981aa66d79364be8322a9c358aaf0a571cca0873" || source.UpstreamVectorDirectory != "tests" {
		t.Fatalf("unexpected vector source: %+v", source)
	}
	actual := map[string]string{}
	methods := map[string]bool{}
	if err := fs.WalkDir(files, "vectors", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".io") {
			return nil
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(name, "vectors/")
		actual[relative] = digest(data)
		methods[path.Dir(relative)] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 21 || !reflect.DeepEqual(actual, source.Vectors) {
		t.Fatalf("shared vector bytes or method selection differ: %d methods, %d files", len(methods), len(actual))
	}
}

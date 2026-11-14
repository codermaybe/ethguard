package wire_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func TestSimulateOfficialVectorProvenance(t *testing.T) {
	evidence := spec.SimulateV1EvidenceFS()
	raw, err := fs.ReadFile(evidence, "simulatev1/SOURCE.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Repository, Version, Commit, Method string
		BundleSHA256                        string
		UpstreamVectorDirectory             string
		Vectors                             map[string]string
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if source.Repository != "https://github.com/ethereum/execution-apis" ||
		source.Version != spec.Version() ||
		source.Commit != "981aa66d79364be8322a9c358aaf0a571cca0873" ||
		source.Method != "eth_simulateV1" ||
		source.UpstreamVectorDirectory != "tests/eth_simulateV1" {
		t.Fatalf("unexpected source identity: %+v", source)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(spec.SimulateV1JSON())); got != source.BundleSHA256 {
		t.Fatalf("official bundle digest = %s, want %s", got, source.BundleSHA256)
	}
	actual := map[string]string{}
	err = fs.WalkDir(evidence, "simulatev1/vectors", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(evidence, name)
		if err != nil {
			return err
		}
		actual[path.Base(name)] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 95 || !reflect.DeepEqual(actual, source.Vectors) {
		t.Fatal("official vector inventory or bytes differ from the pinned source manifest")
	}
}

func TestSimulateOfficialResultsPreserveDeclaredFields(t *testing.T) {
	schema := officialResultSchemas(t)["eth_simulateV1"]
	parse := resultConstructor(ethapi.NewSimulateV1Result)
	total, topErrors, successes := 0, 0, 0
	evidence := spec.SimulateV1EvidenceFS()
	err := fs.WalkDir(evidence, "simulatev1/vectors", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(evidence, name)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			total++
			if vector.Method != "eth_simulateV1" {
				t.Fatalf("%s: unexpected method %q", vector.Identity(), vector.Method)
			}
			if vector.Error != nil {
				topErrors++
				continue
			}
			t.Run(vector.Identity(), func(t *testing.T) {
				expected := simulateDeclaredFields(t, vector.Result, schema)
				assertOfficialProjectionRoundTrip(t, parse, schema, vector.Result, expected)
				successes++
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 95 || topErrors != 31 || successes != 64 {
		t.Fatalf("official corpus accounting: total=%d, top-level errors=%d, successful results=%d", total, topErrors, successes)
	}
}

// The pinned source vectors include failure-call logs and error.data although
// neither is declared by their failure schemas. Compare the complete declared
// projection while still parsing the ORIGINAL input. Native execution must
// deliver these additional fields through its extensions/raw-evidence channels.
// No field is added to the official model to accommodate these samples.
func simulateDeclaredFields(t *testing.T, raw json.RawMessage, schema any) json.RawMessage {
	t.Helper()
	resultProperties := schema.(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	callBranches := resultProperties["calls"].(map[string]any)["items"].(map[string]any)["oneOf"].([]any)
	failureProperties := callBranches[0].(map[string]any)["properties"].(map[string]any)
	if _, declared := failureProperties["logs"]; declared {
		t.Fatal("failure-call logs are now official; retire this projection")
	}
	for _, branch := range failureProperties["error"].(map[string]any)["oneOf"].([]any) {
		if _, declared := branch.(map[string]any)["properties"].(map[string]any)["data"]; declared {
			t.Fatal("failure error.data is now official; retire this projection")
		}
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		var calls []map[string]json.RawMessage
		if err := json.Unmarshal(block["calls"], &calls); err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if string(call["status"]) != "\"0x0\"" {
				continue
			}
			delete(call, "logs")
			var failure map[string]json.RawMessage
			if err := json.Unmarshal(call["error"], &failure); err != nil {
				t.Fatal(err)
			}
			delete(failure, "data")
			data, err := json.Marshal(failure)
			if err != nil {
				t.Fatal(err)
			}
			call["error"] = data
		}
		data, err := json.Marshal(calls)
		if err != nil {
			t.Fatal(err)
		}
		block["calls"] = data
	}
	expected, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return expected
}

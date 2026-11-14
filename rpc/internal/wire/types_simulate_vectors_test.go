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
		source.Commit != "5aebdfdd45cadeb723be4bd45b4611b71c8b1c85" ||
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
	if len(actual) != 91 || !reflect.DeepEqual(actual, source.Vectors) {
		t.Fatal("official vector inventory or bytes differ from the pinned source manifest")
	}
}

func TestSimulateOfficialResultsPreserveDeclaredFieldsAndExposeSourceConflicts(t *testing.T) {
	schema := officialResultSchemas(t)["eth_simulateV1"]
	parse := resultConstructor(ethapi.NewSimulateV1Result)
	// These are source conflicts, not accepted results or a waiver of schema
	// validation. Each original must fail at the known location, and changing
	// only the conflicting message in a TEST COPY must produce a full success.
	conflicts := map[string]struct {
		block, call, messages int
	}{
		"ethSimulate-run-out-of-gas-in-block-38015.io":    {1, 1, 1},
		"ethSimulate-use-as-many-features-as-possible.io": {9, 0, 12},
	}
	seenConflicts := map[string]bool{}
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
				// Error responses exercise no result parser. Native execution
				// must establish their classification in a later method slice.
				topErrors++
				continue
			}
			t.Run(vector.Identity(), func(t *testing.T) {
				conflict, known := conflicts[path.Base(name)]
				if !known {
					expected := simulateDeclaredFields(t, vector.Result, schema)
					assertOfficialProjectionRoundTrip(t, parse, schema, vector.Result, expected)
					successes++
					return
				}
				seenConflicts[path.Base(name)] = true
				if err := conformance.ValidateResult(schema, vector.Result); err == nil {
					t.Fatal("upstream vector now conforms; retire its conflict record")
				}
				_, err := ethapi.NewSimulateV1Result(vector.Result)
				want := fmt.Sprintf("eth_simulateV1.result[%d]: eth_simulateV1.result.items.properties.calls[%d]: SimulateCallResult: expected oneOf branch match, got 0", conflict.block, conflict.call)
				if err == nil || err.Error() != want {
					t.Fatalf("original conflict error = %v, want %s", err, want)
				}
				copy, messages := simulateVMMessageTestCopy(t, vector.Result)
				if messages != conflict.messages {
					t.Fatalf("conflicting message count = %d, want %d", messages, conflict.messages)
				}
				expected := simulateDeclaredFields(t, copy, schema)
				assertOfficialProjectionRoundTrip(t, parse, schema, copy, expected)
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 91 || topErrors != 27 || successes != 62 || len(seenConflicts) != len(conflicts) {
		t.Fatalf("official corpus accounting: total=%d, top-level errors=%d, successful results=%d, source conflicts=%v",
			total, topErrors, successes, seenConflicts)
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

// This mutation diagnoses the upstream message-pattern conflict. It is not a
// production repair, does not modify the vendored evidence, and is never counted
// as an original official vector success.
func simulateVMMessageTestCopy(t *testing.T, raw json.RawMessage) (json.RawMessage, int) {
	t.Helper()
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	messages := 0
	for _, block := range blocks {
		var calls []map[string]json.RawMessage
		if err := json.Unmarshal(block["calls"], &calls); err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if string(call["status"]) != "\"0x0\"" {
				continue
			}
			var failure map[string]json.RawMessage
			if err := json.Unmarshal(call["error"], &failure); err != nil {
				t.Fatal(err)
			}
			if string(failure["code"]) != "-32015" || string(failure["message"]) != "\"out of gas\"" {
				t.Fatalf("unexpected failure in conflict fixture: %s", call["error"])
			}
			// Check the conflicting leaf directly, independently of the union
			// parser's aggregate error, before changing this test copy.
			_, err := ethapi.NewSimulateErrorVM(call["error"])
			const want = "eth_simulateV1.result.items.properties.calls.items.oneOf[0].properties.error.oneOf[1].properties.message: does not match official pattern"
			if err == nil || err.Error() != want {
				t.Fatalf("VM message error = %v, want %s", err, want)
			}
			failure["message"] = json.RawMessage("\"vm execution error: out of gas\"")
			call["error"], err = json.Marshal(failure)
			if err != nil {
				t.Fatal(err)
			}
			messages++
		}
		encoded, err := json.Marshal(calls)
		if err != nil {
			t.Fatal(err)
		}
		block["calls"] = encoded
	}
	copy, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return copy, messages
}

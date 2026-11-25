package executionapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

const (
	originalDataPattern = "^0x[0-9a-f]*$"
	wholeBytePattern    = "^0x([0-9a-f]{2})*$"
)

// The fixed source has 64 DATA definitions in 22 independently audited roots.
// These counts intentionally do not infer coverage from the patch inventory.
var dataWidthRoots = map[string]int{
	"result/eth_call":                  1,
	"result/eth_getBlockByHash":        6,
	"result/eth_getBlockByNumber":      6,
	"result/eth_getBlockReceipts":      1,
	"result/eth_getCode":               1,
	"result/eth_getHeaderByHash":       1,
	"result/eth_getHeaderByNumber":     1,
	"result/eth_getLogs":               1,
	"result/eth_getProof":              2,
	"result/eth_getStorageAt":          1,
	"result/eth_getTransactionByHash":  5,
	"result/eth_getTransactionReceipt": 1,
	"result/eth_simulateV1":            9,
	"request/eth_call/0":               4,
	"request/eth_createAccessList/0":   4,
	"request/eth_estimateGas/0":        4,
	"request/eth_fillTransaction/0":    4,
	"request/eth_sendRawTransaction/0": 1,
	"request/eth_sendTransaction/0":    4,
	"request/eth_sign/1":               1,
	"request/eth_signTransaction/0":    4,
	"request/eth_simulateV1/0":         2,
}

func schemaRoots(bundle Bundle) map[string]json.RawMessage {
	roots := make(map[string]json.RawMessage, len(bundle.Results)+len(bundle.Requests))
	for method, raw := range bundle.Results {
		roots["result/"+method] = raw
	}
	for method, parameters := range bundle.Requests {
		for i, parameter := range parameters {
			roots[fmt.Sprintf("request/%s/%d", method, i)] = parameter.Schema
		}
	}
	return roots
}

func visitSchemaObjects(value any, visit func(map[string]any)) {
	switch value := value.(type) {
	case map[string]any:
		visit(value)
		for _, child := range value {
			visitSchemaObjects(child, visit)
		}
	case []any:
		for _, child := range value {
			visitSchemaObjects(child, visit)
		}
	}
}

func selectedErrata(t *testing.T, dataWidth bool) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	entries, err := fs.ReadDir(spec.ErrataFS(), "errata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), "wire-data-") != dataWidth {
			continue
		}
		name := "errata/" + entry.Name()
		raw, err := fs.ReadFile(spec.ErrataFS(), name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: raw}
	}
	return files
}

func beforeDataWidthErrata(t *testing.T) Bundle {
	t.Helper()
	bundle, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyErrata(&bundle, spec.SourceJSON(), selectedErrata(t, false)); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestDataWidthErrataCoversOnlyReviewedPatterns(t *testing.T) {
	before := schemaRoots(beforeDataWidthErrata(t))
	effective, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	after := schemaRoots(effective)
	if len(before) != len(after) {
		t.Fatal("DATA correction changed root coverage")
	}
	got := map[string]int{}
	for name, raw := range before {
		original := decodeSchemaObject(t, raw)
		corrected := decodeSchemaObject(t, after[name])
		originalCount := 0
		visitSchemaObjects(original, func(node map[string]any) {
			if node["pattern"] == originalDataPattern {
				originalCount++
			}
		})
		correctedCount := 0
		visitSchemaObjects(corrected, func(node map[string]any) {
			if node["pattern"] == wholeBytePattern {
				correctedCount++
				node["pattern"] = originalDataPattern
			}
		})
		if originalCount != correctedCount {
			t.Errorf("%s: source DATA nodes=%d corrected=%d", name, originalCount, correctedCount)
		}
		if correctedCount != 0 {
			got[name] = correctedCount
		}
		if !reflect.DeepEqual(original, corrected) {
			t.Errorf("%s: DATA correction changed another schema rule", name)
		}
	}
	if !reflect.DeepEqual(got, dataWidthRoots) {
		t.Fatalf("DATA roots:\n got %#v\nwant %#v", got, dataWidthRoots)
	}
	if got := len(selectedErrata(t, true)); got != len(dataWidthRoots) {
		t.Fatalf("DATA errata documents=%d, want %d independently audited roots", got, len(dataWidthRoots))
	}
}

func TestEffectiveDataSchemasRequireWholeBytes(t *testing.T) {
	effective, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	for root, raw := range schemaRoots(effective) {
		t.Run(root, func(t *testing.T) {
			count := 0
			visitSchemaObjects(decodeSchemaObject(t, raw), func(node map[string]any) {
				if node["pattern"] != wholeBytePattern {
					return
				}
				count++
				for _, value := range []string{`"0x"`, `"0x00"`, `"0x0001"`, `"0xf0f0"`} {
					if err := conformance.ValidateResult(node, json.RawMessage(value)); err != nil {
						t.Errorf("valid complete bytes %s: %v", value, err)
					}
				}
				for _, value := range []string{`"0x0"`, `"0xf0f"`, `"0X00"`, `"0xA0"`, `null`, `12`} {
					if err := conformance.ValidateResult(node, json.RawMessage(value)); err == nil {
						t.Errorf("effective DATA accepted %s", value)
					}
				}
			})
			if count != dataWidthRoots[root] {
				t.Errorf("effective DATA nodes=%d, want %d", count, dataWidthRoots[root])
			}
		})
	}
	// The pinned upstream oracle still exposes its original omission. This
	// distinction must survive even when generated values reject partial bytes.
	original, err := ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"0x0"`, `"0xf0f"`} {
		if err := conformance.ValidateResult(decodeSchemaObject(t, original.Results["eth_getCode"]), json.RawMessage(value)); err != nil {
			t.Fatalf("upstream DATA rule changed; review/retire correction: %v", err)
		}
	}
}

func TestDataWidthErrataRejectsLaterDriftAtomically(t *testing.T) {
	bundle := beforeDataWidthErrata(t)
	files := selectedErrata(t, true)
	if len(files) != len(dataWidthRoots) {
		t.Fatalf("incomplete DATA correction inventory: %d", len(files))
	}
	// This target occurs late in the ordered inventory, after earlier patches
	// would have succeeded. A byte-only drift still requires review.
	params := bundle.Requests["eth_simulateV1"]
	params[0].Schema = append(append(json.RawMessage(nil), params[0].Schema...), ' ')
	before := mustJSON(t, bundle)
	err := applyErrata(&bundle, spec.SourceJSON(), files)
	if err == nil || !strings.Contains(err.Error(), "input schema digest") {
		t.Fatalf("DATA source drift error = %v", err)
	}
	if !bytes.Equal(before, mustJSON(t, bundle)) {
		t.Fatal("failed DATA correction delivered partially patched reference data")
	}
}

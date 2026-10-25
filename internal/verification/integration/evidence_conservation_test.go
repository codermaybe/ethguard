package ethguard_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// T09.2 conservation traces every field of every observation, including every
// array element and nested object, to one declared destination. Nothing is
// checked only on payload[0] or only at the top level.

// TestVerificationEvidenceConservationPerObservation assigns one representation variant to
// each source and traces each source's own payload into the canonical Value.
// Local methods select one source, so each variant is exercised alone.
func TestVerificationEvidenceConservationPerObservation(t *testing.T) {
	for _, row := range t09FieldRows(t) {
		t.Run(row.method, func(t *testing.T) {
			if row.resolution == t09Local {
				for index, variant := range row.variants {
					fleet := t09NewFleet(t, 1)
					fleet.returnOne(row.method, variant)
					decision, err := fleet.client.Call(fleet.context(t), row.method, row.params...)
					if err != nil {
						t.Fatalf("variant %d: %v", index, err)
					}
					t09TraceConservation(t, row, variant, decision.Value, decision)
				}
				return
			}
			fleet := t09NewFleet(t, len(row.variants))
			for index, variant := range row.variants {
				fleet.nodes[index].ReturnRaw(row.method, variant)
			}
			decision, err := fleet.client.Call(fleet.context(t), row.method, row.params...)
			if err != nil {
				t.Fatalf("variants: %v", err)
			}
			for _, variant := range row.variants {
				t09TraceConservation(t, row, variant, decision.Value, decision)
			}
		})
	}
}

// TestVerificationEvidenceOutsideDiagnosticsPerObservation gives each source a distinct
// out-of-domain value. Every value must stay out of Value and appear in
// Diagnostics with exactly its own source, so attribution is per observation,
// not borrowed from the winning response.
func TestVerificationEvidenceOutsideDiagnosticsPerObservation(t *testing.T) {
	for _, row := range t09FieldRows(t) {
		if !row.projector || !containsString(row.outside, "vendor") {
			continue
		}
		t.Run(row.method, func(t *testing.T) {
			fleet := t09NewFleet(t, len(row.variants))
			path := "/vendor"
			for index, variant := range row.variants {
				if strings.HasPrefix(strings.TrimSpace(string(variant)), "[") {
					path = "/0/vendor"
					variant = t09SetNestedField(t, variant, 0, "vendor", fleet.names[index])
				} else {
					variant = t09SetField(t, variant, "vendor", fleet.names[index])
				}
				fleet.nodes[index].ReturnRaw(row.method, variant)
			}
			decision, err := fleet.client.Call(fleet.context(t), row.method, row.params...)
			if err != nil {
				t.Fatal(err)
			}
			if bytesContain(decision.Value, []byte(`"vendor"`)) {
				t.Fatalf("vendor entered Value: %s", trimValue(decision.Value))
			}
			seen := 0
			for _, residual := range decision.Diagnostics.Residuals {
				if residual.Path != path {
					continue
				}
				seen++
				if len(residual.Sources) != 1 {
					t.Fatalf("residual %s has sources %v, want exactly one", path, t09SourceNames(residual.Sources))
				}
				name := residual.Sources[0].Name
				var value string
				if err := json.Unmarshal(residual.Value, &value); err != nil || value != name {
					t.Fatalf("residual %s value = %s, want %q", path, residual.Value, name)
				}
			}
			if seen != len(row.variants) {
				t.Fatalf("residuals for %s = %d, want one per observation %d: %+v", path, seen, len(row.variants), decision.Diagnostics.Residuals)
			}
		})
	}
}

// TestVerificationEvidenceNestedDerivedFieldRecomputation covers a nested protected object
// whose JSON hash is derived, not authored: a tampered transaction hash inside
// a block body does not enter Value. The canonical encoder recomputes it from
// the signed transaction, and the source remains a supporter because the
// transaction root still agrees.
func TestVerificationEvidenceNestedDerivedFieldRecomputation(t *testing.T) {
	chain := t09NewChain(t)
	block, hash := chain.block(t09AnchorHeight, 1_700_000_000, 21_000)
	tampered := "0x" + strings.Repeat("77", 32)
	transactions := t09SetNestedField(t, fieldOf(t, block, "transactions"), 0, "hash", tampered)
	payload := t09SetField(t, block, "transactions", transactions)
	fleet := t09NewFleet(t, 3)
	fleet.returnOne(t09FieldRowFor(t, "eth_getBlockByHash").method, payload)
	decision, err := fleet.client.Call(fleet.context(t), "eth_getBlockByHash", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
		t.Fatalf("decision = %+v", decision)
	}
	if bytesContain(decision.Value, []byte(tampered)) {
		t.Fatalf("tampered nested hash entered Value")
	}
	var decoded struct {
		Transactions []struct {
			Hash string `json:"hash"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(decision.Value, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Transactions) != 2 || decoded.Transactions[0].Hash != chain.txHash || decoded.Transactions[1].Hash != chain.txHash2 {
		t.Fatalf("derived transaction hashes = %+v, want %q and %q", decoded.Transactions, chain.txHash, chain.txHash2)
	}
}

// t09TraceConservation recursively compares one observation with Value.
func t09TraceConservation(t *testing.T, row t09FieldRow, payload, value json.RawMessage, decision rpc.Decision) {
	t.Helper()
	rawTree := decodeJSONTree(t, t09SemanticJSON(t, payload))
	valueTree := decodeJSONTree(t, value)
	extensions := extensionPaths(decision)
	residuals := residualPaths(decision)
	var walk func(path string, raw, canonical any)
	walk = func(path string, raw, canonical any) {
		switch rawValue := raw.(type) {
		case map[string]any:
			canonicalObject, ok := canonical.(map[string]any)
			if !ok {
				t.Fatalf("%s: raw is an object, Value is %T", path, canonical)
			}
			for key, rawChild := range rawValue {
				childPath := joinPath(path, key)
				if t09OutsideField(row, key) {
					if _, present := canonicalObject[key]; present {
						t.Fatalf("%s: outside field entered Value", childPath)
					}
					t09AssertOutsideDestination(t, row, childPath, rawChild, extensions, residuals)
					continue
				}
				canonicalChild, present := canonicalObject[key]
				if rawChild == nil {
					if row.nullPreserved {
						if !present || canonicalChild != nil {
							t.Fatalf("%s: null protected field was not preserved", childPath)
						}
					} else if present {
						t.Fatalf("%s: null protected field entered Value", childPath)
					}
					continue
				}
				if !present {
					t.Fatalf("%s: protected field disappeared from Value", childPath)
				}
				walk(childPath, rawChild, canonicalChild)
			}
			for key := range canonicalObject {
				if _, present := rawValue[key]; !present && !t09OutsideField(row, key) {
					t.Fatalf("%s: Value carries field %q absent from the observation", path, key)
				}
			}
		case []any:
			canonicalArray, ok := canonical.([]any)
			if !ok {
				t.Fatalf("%s: raw is an array, Value is %T", path, canonical)
			}
			if len(rawValue) != len(canonicalArray) {
				t.Fatalf("%s: array length %d in raw, %d in Value", path, len(rawValue), len(canonicalArray))
			}
			for index := range rawValue {
				walk(fmt.Sprintf("%s/%d", path, index), rawValue[index], canonicalArray[index])
			}
		default:
			if !semanticJSONEqual(raw, canonical) {
				t.Fatalf("%s: Value %v differs from observation %v", path, canonical, raw)
			}
		}
	}
	walk("", rawTree, valueTree)
	if row.whole {
		t09AssertWholeCanonical(t, row.method, payload, decision.Value)
	}
}

func t09OutsideField(row t09FieldRow, key string) bool {
	return containsString(row.outside, key) || containsString(row.omitted, key)
}

func t09OmittedField(row t09FieldRow, key string) bool {
	return containsString(row.omitted, key)
}

type t09ObservedField struct {
	value   json.RawMessage
	sources []rpc.SourceRef
}

func t09AssertOutsideDestination(t *testing.T, row t09FieldRow, path string, raw any, extensions, residuals map[string]t09ObservedField) {
	t.Helper()
	omittedKey := strings.TrimPrefix(path[strings.LastIndex(path, "/"):], "/")
	if t09OmittedField(row, omittedKey) || !row.projector {
		// Diagnostics may still carry a non-quorum spelling as evidence; the
		// omission claim is about the accepted set, which must not promote it
		// to an extension.
		if _, present := extensions[path]; present {
			t.Fatalf("%s: encoder-omitted field unexpectedly became an extension", path)
		}
		return
	}
	field, ok := extensions[path]
	if !ok {
		field, ok = residuals[path]
	}
	if !ok {
		keys := make([]string, 0, len(extensions)+len(residuals))
		for key := range extensions {
			keys = append(keys, key)
		}
		for key := range residuals {
			keys = append(keys, key)
		}
		t.Fatalf("%s: outside field has no extension or diagnostic destination (have %v)", path, keys)
	}
	var observed any
	if err := json.Unmarshal(field.value, &observed); err != nil {
		t.Fatalf("%s: destination value: %v", path, err)
	}
	if !semanticJSONEqual(raw, observed) {
		t.Fatalf("%s: destination value %v differs from observation %v", path, observed, raw)
	}
}

func extensionPaths(decision rpc.Decision) map[string]t09ObservedField {
	out := map[string]t09ObservedField{}
	if decision.Extensions != nil {
		for _, field := range decision.Extensions.Fields {
			out[field.Path] = t09ObservedField{value: field.Value, sources: field.Sources}
		}
	}
	return out
}

func residualPaths(decision rpc.Decision) map[string]t09ObservedField {
	out := map[string]t09ObservedField{}
	if decision.Diagnostics != nil {
		for _, field := range decision.Diagnostics.Residuals {
			out[field.Path] = t09ObservedField{value: field.Value, sources: field.Sources}
		}
	}
	return out
}

func decodeJSONTree(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", trimValue(raw), err)
	}
	return value
}

func joinPath(path, key string) string {
	key = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	return path + "/" + key
}

func semanticJSONEqual(left, right any) bool {
	switch leftValue := left.(type) {
	case nil:
		return right == nil
	case bool:
		rightValue, ok := right.(bool)
		return ok && leftValue == rightValue
	case json.Number:
		rightValue, ok := right.(json.Number)
		if !ok {
			return false
		}
		return compareNumbers(string(leftValue), string(rightValue))
	case string:
		rightValue, ok := right.(string)
		return ok && semanticStringEqual(leftValue, rightValue)
	default:
		return false
	}
}

func semanticStringEqual(left, right string) bool {
	if left == right {
		return true
	}
	leftHex, leftOK := parseHexQuantity(left)
	rightHex, rightOK := parseHexQuantity(right)
	if leftOK && rightOK {
		return leftHex.Cmp(rightHex) == 0
	}
	if isDecimal(left) && isDecimal(right) {
		leftValue, leftOK := new(big.Int).SetString(left, 10)
		rightValue, rightOK := new(big.Int).SetString(right, 10)
		return leftOK && rightOK && leftValue.Cmp(rightValue) == 0
	}
	return false
}

func parseHexQuantity(text string) (*big.Int, bool) {
	if len(text) < 3 || (text[:2] != "0x" && text[:2] != "0X") {
		return nil, false
	}
	value, ok := new(big.Int).SetString(text[2:], 16)
	return value, ok
}

func isDecimal(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func compareNumbers(left, right string) bool {
	leftValue, ok := new(big.Rat).SetString(left)
	if !ok {
		return left == right
	}
	rightValue, ok := new(big.Rat).SetString(right)
	return ok && leftValue.Cmp(rightValue) == 0
}

func fieldOf(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object[key]
}

func bytesContain(raw, needle []byte) bool {
	return strings.Contains(string(raw), string(needle))
}

func trimValue(raw json.RawMessage) string {
	if len(raw) > 120 {
		return string(raw[:120]) + "..."
	}
	return string(raw)
}

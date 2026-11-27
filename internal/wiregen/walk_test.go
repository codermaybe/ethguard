package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec"
)

func testSchema(t *testing.T, raw string) *schema {
	t.Helper()
	n, err := parseSchema([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func testManifest() executionapi.Manifest {
	manifest, err := executionapi.LoadManifest("../executionapi/manifest.json")
	if err != nil {
		panic(err)
	}
	return manifest
}
func testRegistry() *registry {
	return newRegistry(testManifest(), &genReport{visited: map[string]bool{}})
}

func TestConstraintsFollowOptionalAndContainerValues(t *testing.T) {
	reg := testRegistry()
	_, err := reg.walk(testSchema(t, `{"type":"object","properties":{"code":{"const":-32015},"message":{"type":"string","pattern":"^allowed$"},"rows":{"type":"array","items":{"const":3}},"nullable":{"oneOf":[{"type":"null"},{"type":"string","pattern":"^kept$"}]}}}`), "example.result")
	if err != nil {
		t.Fatal(err)
	}
	obj := reg.objects[0]
	fields := map[string]fieldModel{}
	for _, f := range obj.fields {
		fields[f.json] = f
	}
	if fields["code"].req || !fields["code"].ref.hasConst || fields["code"].ref.constant != json.Number("-32015") {
		t.Fatal("optional const did not remain on its value")
	}
	if fields["message"].ref.pattern != "^allowed$" {
		t.Fatal("optional string pattern lost")
	}
	if fields["rows"].ref.elem.constant != json.Number("3") {
		t.Fatal("array member const lost")
	}
	nullable := fields["nullable"]
	if !nullable.ref.pointer || nullable.ref.inner.pattern != "^kept$" {
		t.Fatal("nullable branch constraint lost")
	}
	if fmt.Sprintf("%#v", fieldTypeCode(nullable)) != "**Text" {
		t.Fatal("optional nullable value cannot distinguish absence from null")
	}
	root, err := reg.walk(testSchema(t, `{"type":"string","pattern":"^root$"}`), "scalar.result")
	if err != nil || root.pattern != "^root$" {
		t.Fatalf("scalar root constraint lost: %v", err)
	}
}

func TestNumberModelKeepsArbitraryExactBounds(t *testing.T) {
	reg := testRegistry()
	n := testSchema(t, `{"type":"number","minimum":-9007199254740993.0000000000000001,"maximum":9.999999999999999999999e200}`)
	r, err := reg.walk(n, "precise.result")
	if err != nil {
		t.Fatal(err)
	}
	if r.native != "Number" || r.minimum.String() != "-9007199254740993.0000000000000001" || r.maximum.String() != "9.999999999999999999999e200" {
		t.Fatalf("number model lost exact schema bounds: %+v", r)
	}
	unbounded, err := reg.walk(testSchema(t, `{"type":"number"}`), "unbounded.result")
	if err != nil || unbounded.native != "Number" || unbounded.minimum != nil || unbounded.maximum != nil {
		t.Fatalf("unbounded number was narrowed: %v", err)
	}
	constant, err := reg.walk(testSchema(t, `{"type":"number","const":1.0000000000000000000000001}`), "constant.result")
	if err != nil || constant.constant != json.Number("1.0000000000000000000000001") {
		t.Fatalf("numeric const lost precision: %v", err)
	}
}

func TestIntegerConstantsUsePortableRepresentation(t *testing.T) {
	for _, tc := range []struct {
		value string
		inInt bool
	}{
		{value: "-2147483649"},
		{value: "-2147483648", inInt: true},
		{value: "2147483647", inInt: true},
		{value: "2147483648"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			constant, err := testRegistry().walk(testSchema(t, `{"const":`+tc.value+`}`), "constant.result")
			if err != nil {
				t.Fatal(err)
			}
			wantNative := "Number"
			if tc.inInt {
				wantNative = "int"
			}
			if constant.native != wantNative || constant.constant != json.Number(tc.value) {
				t.Fatalf("untyped constant = %+v, want %s with value %s", constant, wantNative, tc.value)
			}
			integer, err := testRegistry().walk(testSchema(t, `{"type":"integer","const":`+tc.value+`}`), "integer.result")
			if !tc.inInt {
				if err == nil || !strings.Contains(err.Error(), "integer.result: unsupported integer const") {
					t.Fatalf("out-of-range integer: %v", err)
				}
				return
			}
			if err != nil || integer.native != "int" || integer.constant != json.Number(tc.value) {
				t.Fatalf("integer constant = %+v, error = %v", integer, err)
			}
		})
	}
}

func TestUnsupportedSemanticsFailAtTheirSchemaPath(t *testing.T) {
	cases := []struct{ name, raw, path string }{
		{"allOf", `{"allOf":[{"type":"string"}]}`, "example.result"},
		{"multiple compositions", `{"oneOf":[{"type":"string"}],"anyOf":[{"type":"boolean"}]}`, "example.result"},
		{"bounds on text", `{"type":"string","minimum":0}`, "example.result"},
		{"compound const", `{"const":{"x":1}}`, "example.result"},
		{"null const", `{"const":null}`, "example.result"},
		{"unconsumed required", `{"type":"boolean","required":["x"]}`, "example.result"},
		{"unknown nested keyword", `{"type":"array","items":{"type":"string","minLength":2}}`, "example.result/items"},
		{"composition path", `{"anyOf":[{"type":"null"},{"type":"string"},{"type":"number","exclusiveMaximum":2}]}`, "example.result/anyOf/2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testRegistry().walk(testSchema(t, tc.raw), "example.result")
			if err == nil || !strings.Contains(err.Error(), tc.path+":") {
				t.Fatalf("wanted rejection at %s, got %v", tc.path, err)
			}
		})
	}
}

func TestFingerprintRetainsSemanticDifferences(t *testing.T) {
	pairs := [][2]string{
		{`{"oneOf":[{"type":"string"},{"type":"boolean"}]}`, `{"anyOf":[{"type":"string"},{"type":"boolean"}]}`},
		{`{"type":"number","minimum":0,"maximum":1}`, `{"type":"number","minimum":0,"maximum":2}`},
		{`{"type":"string","const":"a"}`, `{"type":"string","const":"b"}`},
		{`{"type":"string","pattern":"^a$"}`, `{"type":"string","pattern":"^b$"}`},
		{`{"type":"string"}`, `{"oneOf":[{"type":"null"},{"type":"string"}]}`},
		{`{"type":"object","properties":{"x":{"type":"string"}}}`, `{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`},
	}
	for _, pair := range pairs {
		if fingerprint(testSchema(t, pair[0])) == fingerprint(testSchema(t, pair[1])) {
			t.Fatalf("semantically distinct schemas deduplicated: %s and %s", pair[0], pair[1])
		}
	}
	a := testSchema(t, `{"type":"object","title":"First","properties":{"x":{"type":"string"},"y":{"type":"boolean"}},"required":["x","y"]}`)
	b := testSchema(t, `{"type":"object","title":"Other","properties":{"y":{"type":"boolean"},"x":{"type":"string"}},"required":["y","x"]}`)
	if fingerprint(a) != fingerprint(b) {
		t.Fatal("presentation differences changed semantic identity")
	}
}

func TestCompositionKeepsOriginalBranchIndexes(t *testing.T) {
	reg := testRegistry()
	_, err := reg.walk(testSchema(t, `{"anyOf":[{"type":"null"},{"type":"string","const":"yes"},{"type":"boolean","const":false}]}`), "indexed.result")
	if err != nil {
		t.Fatal(err)
	}
	u := reg.unionList[0]
	if u.kind != "anyOf" || !u.nullable {
		t.Fatal("composition kind or null branch lost")
	}
	for i, b := range u.branches {
		want := i + 1
		if b.index != want || b.ref.path != "indexed.result/anyOf/"+itoa(want) {
			t.Fatalf("branch %d lost its original index/path: %+v", want, b)
		}
		if !b.ref.hasConst {
			t.Fatalf("branch %d lost its complete constraint", want)
		}
	}
}

func TestPinnedNamingUsesRealAnyOfPaths(t *testing.T) {
	var bundle struct {
		Results map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(spec.MethodsJSON(), &bundle); err != nil {
		t.Fatal(err)
	}
	n, err := parseSchema(bundle.Results["eth_getBlockByHash"])
	if err != nil {
		t.Fatal(err)
	}
	reg := testRegistry()
	if _, err := reg.walk(n, "eth_getBlockByHash.result"); err != nil {
		t.Fatal(err)
	}
	path := "eth_getBlockByHash.result/oneOf/1/properties/transactions/anyOf/1/items/oneOf/0/properties/authorizationList/items"
	if !reg.report.visited[path] {
		t.Fatal("official anyOf authorization naming override was not consumed")
	}
	var transactions *unionModel
	for _, u := range reg.unionList {
		if u.name == "BlockTransactions" {
			transactions = u
		}
	}
	if transactions == nil || transactions.kind != "anyOf" {
		t.Fatal("pinned BlockTransactions no longer retains its official anyOf")
	}
}

func TestBoundedConstraintVocabularyRejectsUnsupportedForms(t *testing.T) {
	for _, raw := range []string{
		`{"type":"string","enum":[]}`,
		`{"enum":["x"]}`,
		`{"type":"string","enum":[{"x":1}]}`,
		`{"type":"object","not":{"properties":{"x":{"type":"string"}}},"properties":{"x":{"type":"string"}}}`,
		`{"type":"object","not":{"type":"object","required":["x"]},"properties":{"x":{"type":"string"}}}`,
		`{"type":"object","patternProperties":{"a":{"type":"string"},"b":{"type":"number"}},"additionalProperties":false}`,
		`{"type":"object","patternProperties":{"a":{"type":"string"}}}`,
		`{"type":"object","properties":{"x":{"type":"string"}},"patternProperties":{"a":{"type":"string"}},"additionalProperties":false}`,
		`{"type":"array","minItems":2,"maxItems":1,"items":{"type":"string"}}`,
		`{"type":"string","minItems":1}`,
		`{"type":"object","properties":{"additionalProperties":{"type":"string"}}}`,
		`{"type":"object","not":{"required":["x"]},"oneOf":[{"type":"object","properties":{"x":{"type":"string"}}}],"properties":{"x":{"type":"string"}}}`,
	} {
		reg := testRegistry()
		reg.strictRequests = true
		if _, err := reg.walk(testSchema(t, raw), "eth_generic.params[0]"); err == nil {
			t.Errorf("unsupported schema accepted: %s", raw)
		}
	}
}

func TestConstraintFingerprintsAndRequestProjectionModesStayDistinct(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"type":"string","enum":["a"]}`, `{"type":"string","enum":["b"]}`},
		{`{"type":"object","properties":{"x":{"type":"string"}},"not":{"required":["x"]}}`, `{"type":"object","properties":{"x":{"type":"string"}},"not":{"required":["y"]}}`},
		{`{"type":"array","items":{"type":"number"},"uniqueItems":true}`, `{"type":"array","items":{"type":"number"}}`},
		{`{"type":"array","items":{"type":"number"},"maxItems":1}`, `{"type":"array","items":{"type":"number"},"maxItems":2}`},
		{`{"type":"object","patternProperties":{"^a":{"type":"string"}},"additionalProperties":false}`, `{"type":"object","patternProperties":{"^b":{"type":"string"}},"additionalProperties":false}`},
	} {
		if fingerprint(testSchema(t, pair[0])) == fingerprint(testSchema(t, pair[1])) {
			t.Fatalf("constraint difference was deduplicated: %v", pair)
		}
	}
	reg := testRegistry()
	reg.namesByPath["eth_generic.result"] = "SharedResult"
	reg.namesByPath["eth_generic.params[0]"] = "SharedRequest"
	schema := testSchema(t, `{"type":"object","title":"Shared","properties":{"x":{"type":"string"}},"additionalProperties":false}`)
	result, err := reg.walk(schema, "eth_generic.result")
	if err != nil {
		t.Fatal(err)
	}
	reg.strictRequests = true
	request, err := reg.walk(schema, "eth_generic.params[0]")
	if err != nil {
		t.Fatal(err)
	}
	if result.typeName == request.typeName || request.parser != "parse"+request.typeName {
		t.Fatal("request strict construction reused result projection constructor")
	}
}

func TestArrayAssertionSyntaxDoesNotTreatNullAsZeroOrFalse(t *testing.T) {
	for _, raw := range []string{`{"type":"array","minItems":null}`, `{"type":"array","maxItems":null}`, `{"type":"array","uniqueItems":null}`, `{"type":null,"const":"x"}`, `{"type":"array","minItems":-1}`, `{"type":"array","maxItems":1.5}`} {
		if _, err := parseSchema([]byte(raw)); err == nil {
			t.Errorf("invalid schema assertion accepted: %s", raw)
		}
	}
}

func TestUniqueItemsPreservesStringsAndRejectsProjectedOpenObjects(t *testing.T) {
	reg := testRegistry()
	open := testSchema(t, `{"type":"array","uniqueItems":true,"items":{"type":"object","properties":{"x":{"type":"string"}}}}`)
	if _, err := reg.walk(open, "eth_generic.result"); err == nil || !strings.Contains(err.Error(), "projected open object") {
		t.Fatalf("uniqueItems lost open-object identity: %v", err)
	}
	reg.namesByPath["eth_generic.result"] = "OrdinaryHolder"
	reg.namesByPath["eth_unique.result/items"] = "PreservedHolder"
	address := testSchema(t, `{"type":"object","title":"Address holder","additionalProperties":false,"properties":{"address":{"type":"string","pattern":"^0x[0-9a-fA-F]{40}$"}}}`)
	ordinary, err := reg.walk(address, "eth_generic.result")
	if err != nil {
		t.Fatal(err)
	}
	reg.preserveStrings = true
	preserved, err := reg.walk(address, "eth_unique.result/items")
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.typeName == preserved.typeName {
		t.Fatal("unique string policy was omitted from object fingerprint")
	}
	if reg.objects[0].fields[0].ref.native != "Address" || reg.objects[1].fields[0].ref.native != "Text" {
		t.Fatal("unique string literals were normalized or changed the ordinary projection")
	}
}

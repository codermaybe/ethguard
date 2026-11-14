package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
func testRegistry() *registry { return newRegistry(&genReport{visited: map[string]bool{}}) }

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

func TestUnsupportedSemanticsFailAtTheirSchemaPath(t *testing.T) {
	cases := []struct{ name, raw, path string }{
		{"allOf", `{"allOf":[{"type":"string"}]}`, "example.result"},
		{"multiple compositions", `{"oneOf":[{"type":"string"}],"anyOf":[{"type":"boolean"}]}`, "example.result"},
		{"bounds on text", `{"type":"string","minimum":0}`, "example.result"},
		{"compound const", `{"const":{"x":1}}`, "example.result"},
		{"null const", `{"const":null}`, "example.result"},
		{"unconsumed required", `{"type":"boolean","required":["x"]}`, "example.result"},
		{"unknown nested keyword", `{"type":"array","items":{"type":"string","minLength":2}}`, "example.result.items"},
		{"composition path", `{"anyOf":[{"type":"null"},{"type":"string"},{"type":"number","exclusiveMaximum":2}]}`, "example.result.anyOf[2]"},
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
		if b.index != want || b.ref.path != "indexed.result.anyOf["+itoa(want)+"]" {
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
	path := "eth_getBlockByHash.result.oneOf[1].properties.transactions.anyOf[1].items.oneOf[0].properties.authorizationList.items"
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

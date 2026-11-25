package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Compile and execute freshly emitted code, including shapes not used by the
// pinned result bundle. Model-only tests cannot catch broken literals, pointer
// types, or omitted constraints in the renderer.
func TestEmittedParsersPreserveSchemaSemantics(t *testing.T) {
	reg := testRegistry()
	root, err := reg.walk(testSchema(t, `{
		"type":"object","title":"Emission fixture",
		"required":["rows","selection","empty","exact","bounded"],
		"properties":{
			"rows":{"type":"array","items":{"type":"array","items":{"anyOf":[{"type":"null"},{"type":"number"}]}}},
			"selection":{"title":"Selection","oneOf":[{"type":"string","pattern":"^ok.*"},{"type":"string","const":"ok"}]},
			"empty":{"title":"Empty choice","anyOf":[{"type":"array","items":{"type":"boolean"}},{"type":"array","items":{"type":"string"}}]},
			"exact":{"type":"number","const":1.0000000000000000000000001},
			"bounded":{"type":"number","minimum":0.30000000000000001,"maximum":0.30000000000000002},
			"optional":{"anyOf":[{"type":"null"},{"type":"string","pattern":"^kept$"}]},
			"code":{"const":-32015},
			"html":{"type":"string","const":"<&>"}
		}
	}`), "eth_emitterFixture.result")
	if err != nil {
		t.Fatal(err)
	}
	reg.strictRequests = true
	requestRoot, err := reg.walk(testSchema(t, `{
      "type":"object","title":"Request fixture","required":["choice","rows","dictionary","closed"],
      "not":{"required":["left","right"]},
      "properties":{
       "choice":{"type":"string","enum":["yes","no",null]},
       "literalEnum":{"type":"string","pattern":"^0x[0-9a-fA-F]{40}$","enum":["0xABCDEF0000000000000000000000000000000000"]},
       "exactEnum":{"type":"number","enum":[9007199254740993,1.000000000000000000000001]},
       "uniqueAddresses":{"type":"array","uniqueItems":true,"items":{"type":"string","pattern":"^0x[0-9a-fA-F]{40}$"}},
       "uniqueBytes":{"type":"array","uniqueItems":true,"items":{"type":"string","pattern":"^0x[0-9a-f]{1,2}$"}},
       "uniqueNested":{"type":"array","uniqueItems":true,"items":{"type":"object","title":"Nested unique entry","properties":{"addresses":{"type":"array","items":{"type":"string","pattern":"^0x[0-9a-fA-F]{40}$"}}}}},
       "nullableEnum":{"anyOf":[{"type":"null","enum":[null]},{"type":"string","enum":["kept"]}]},
       "rows":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,"items":{"type":"object","title":"Constrained entry","properties":{"amount":{"type":"number"},"label":{"type":"string"}}}},
       "dictionary":{"type":"object","title":"Pattern dictionary","patternProperties":{"^x":{"type":"string","enum":["ok"]},"x$":{"type":"string","enum":["ok"]}},"additionalProperties":false},
       "closed":{"type":"object","title":"Closed child","properties":{"value":{"type":"string"}},"additionalProperties":false},
       "left":{"type":"string"},"right":{"type":"string"}
      }
    }`), "eth_arbitrary.params[0]")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "ethcodec"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "types.gen.go")
	roots := map[string]*ref{"eth_emitterFixture": root, "eth_arbitrary": requestRoot}
	if err := emit(reg, "emission-test", roots, target); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := emit(reg, "emission-test", roots, target); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("emission changed without an input change")
	}
	for _, name := range []string{"helpers.go", "normalize.go", "numbers.go", "number.go", "equality.go"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", "ethcodec", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "internal", "ethcodec", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	specDir := filepath.Join(dir, "internal", "spec")
	if err := os.MkdirAll(specDir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                "module github.com/codermaybe/ethguard\n\ngo 1.26.10\n",
		"internal/spec/spec.go": "package spec\nfunc Version() string { return \"emission-test\" }\n",
		"emitted_test.go":       emittedBehaviorTest + requestBehaviorTest,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"jsonsafe/jsonsafe.go", "limits/limits.go"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "internal", name))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "internal", name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("freshly generated parser behavior: %v\n%s", err, output)
	}
}

const emittedBehaviorTest = `package ethapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"github.com/codermaybe/ethguard/internal/ethcodec"
)

func TestGeneratedBehavior(t *testing.T) {
	const baseline = "{\"rows\":[[null,0.30000000000000001],[]],\"selection\":\"okay\",\"empty\":[],\"exact\":1.0000000000000000000000001,\"bounded\":0.30000000000000001,\"html\":\"<&>\"}"
	for _, tc := range []struct {
		name, field, value, wantErr string
	}{
		{name:"absent"},
		{name:"null",field:"optional",value:"null"},
		{name:"present",field:"optional",value:"\"kept\""},
		{name:"optional const",field:"code",value:"-32015"},
		{name:"exact equivalent",field:"exact",value:"1.000000000000000000000000100"},
		{name:"upper bound",field:"bounded",value:"0.30000000000000002"},
		{name:"optional pattern",field:"optional",value:"\"lost\"",wantErr:"eth_emitterFixture.result.properties.optional.anyOf[1]: does not match official pattern"},
		{name:"optional constant",field:"code",value:"3",wantErr:"eth_emitterFixture.result.properties.code: expected constant"},
		{name:"exact constant",field:"exact",value:"1",wantErr:"eth_emitterFixture.result.properties.exact: expected constant"},
		{name:"minimum",field:"bounded",value:"0.3",wantErr:"eth_emitterFixture.result.properties.bounded: below official minimum"},
		{name:"maximum",field:"bounded",value:"0.30000000000000003",wantErr:"eth_emitterFixture.result.properties.bounded: above official maximum"},
		{name:"overlapping oneOf",field:"selection",value:"\"ok\"",wantErr:"Selection: expected oneOf branch match, got 2"},
		{name:"unmatched oneOf",field:"selection",value:"\"bad\"",wantErr:"Selection: expected oneOf branch match, got 0"},
		{name:"unmatched anyOf",field:"empty",value:"[1]",wantErr:"EmptyChoice: expected anyOf branch match"},
		{name:"non-null array",field:"rows",value:"null",wantErr:"eth_emitterFixture.result.properties.rows: expected a non-null value"},
		{name:"missing required",field:"rows",wantErr:"EmissionFixture: missing required field rows"},
	} {
		t.Run(tc.name,func(t *testing.T){
			var fields map[string]json.RawMessage
			if err:=json.Unmarshal([]byte(baseline),&fields);err!=nil{t.Fatal(err)}
			if tc.field!="" {
				if tc.value=="" {delete(fields,tc.field)} else {fields[tc.field]=json.RawMessage(tc.value)}
			}
			raw,err:=json.Marshal(fields);if err!=nil{t.Fatal(err)}
			value,err:=NewEmitterFixtureResult(raw)
			if tc.wantErr!="" {
				if err==nil||err.Error()!=tc.wantErr{t.Fatalf("error = %v, want %s",err,tc.wantErr)}
				return
			}
			if err!=nil{t.Fatal(err)}
			out,err:=ethcodec.MarshalNoEscape(value);if err!=nil{t.Fatal(err)}
			var before,after any
			input:=json.NewDecoder(bytes.NewReader(raw));input.UseNumber()
			output:=json.NewDecoder(bytes.NewReader(out));output.UseNumber()
			if err:=input.Decode(&before);err!=nil{t.Fatal(err)}
			if err:=output.Decode(&after);err!=nil{t.Fatal(err)}
			// Exact-equal decimal spellings normalize; all other content and
			// absent/null distinctions must survive unchanged.
			if tc.name=="exact equivalent" {before.(map[string]any)["exact"]=json.Number("1.0000000000000000000000001")}
			if !reflect.DeepEqual(before,after){t.Fatalf("content changed: %s -> %s",raw,out)}
			if !bytes.Contains(out,[]byte("<&>")){t.Fatalf("HTML spelling changed: %s",out)}
			again,err:=NewEmitterFixtureResult(out);if err!=nil{t.Fatal(err)}
			second,err:=ethcodec.MarshalNoEscape(again)
			if err!=nil||!bytes.Equal(out,second){t.Fatalf("not idempotent: %s / %s (%v)",out,second,err)}
		})
	}
}
`

const requestBehaviorTest = `
func TestGeneratedRequestConstraints(t *testing.T) {
 const baseline = "{\"choice\":\"yes\",\"rows\":[{\"amount\":1}],\"dictionary\":{\"x\":\"ok\"},\"closed\":{}}"
 for _,tc:=range []struct{name,field,value string;valid bool}{
  {name:"baseline",valid:true},
  {name:"enum original literal",field:"literalEnum",value:"\"0xABCDEF0000000000000000000000000000000000\"",valid:true},
  {name:"enum rewritten literal",field:"literalEnum",value:"\"0xabcdef0000000000000000000000000000000000\""},
  {name:"enum no",field:"choice",value:"\"no\"",valid:true},
  {name:"enum wrong",field:"choice",value:"\"other\""},
  {name:"enum null",field:"choice",value:"null"},
  {name:"exact number",field:"exactEnum",value:"9007199254740993",valid:true},
  {name:"rounded number",field:"exactEnum",value:"9007199254740992"},
  {name:"exact decimal",field:"exactEnum",value:"1.0000000000000000000000010",valid:true},
  {name:"nullable null",field:"nullableEnum",value:"null",valid:true},
  {name:"nullable member",field:"nullableEnum",value:"\"kept\"",valid:true},
  {name:"nullable nonmember",field:"nullableEnum",value:"\"lost\""},
  {name:"minItems",field:"rows",value:"[]"},
  {name:"maxItems",field:"rows",value:"[{},{},{}]"},
  {name:"unique address case",field:"uniqueAddresses",value:"[\"0xABCDEF0000000000000000000000000000000000\",\"0xabcdef0000000000000000000000000000000000\"]",valid:true},
  {name:"unique byte spelling",field:"uniqueBytes",value:"[\"0x1\",\"0x01\"]",valid:true},
  {name:"unique nested case",field:"uniqueNested",value:"[{\"addresses\":[\"0xABCDEF0000000000000000000000000000000000\"]},{\"addresses\":[\"0xabcdef0000000000000000000000000000000000\"]}]",valid:true},
  {name:"unique original duplicate",field:"uniqueAddresses",value:"[\"0xABCDEF0000000000000000000000000000000000\",\"0xABCDEF0000000000000000000000000000000000\"]"},
  {name:"unique exact equal",field:"rows",value:"[{\"amount\":1},{\"amount\":1.0}]"},
  {name:"unique key order",field:"rows",value:"[{\"amount\":1,\"label\":\"a\"},{\"label\":\"a\",\"amount\":1e0}]"},
  {name:"unique nested extras",field:"rows",value:"[{\"extra\":{\"a\":1,\"b\":2}},{\"extra\":{\"b\":2.0,\"a\":1e0}}]"},
  {name:"unique distinct large",field:"rows",value:"[{\"amount\":9007199254740992},{\"amount\":9007199254740993}]",valid:true},
  {name:"pattern unmatched",field:"dictionary",value:"{\"other\":\"ok\"}"},
  {name:"pattern overlapping",field:"dictionary",value:"{\"x\":\"bad\"}"},
  {name:"pattern second",field:"dictionary",value:"{\"ax\":\"ok\"}",valid:true},
  {name:"closed unknown",field:"closed",value:"{\"unknown\":true}"},
  {name:"open extra",field:"unknown",value:"{\"nested\":[null,900719925474099312345678901234567890]}",valid:true},
 } {
  t.Run(tc.name,func(t *testing.T){
   var fields map[string]json.RawMessage;if err:=json.Unmarshal([]byte(baseline),&fields);err!=nil{t.Fatal(err)}
   if tc.field!=""{fields[tc.field]=json.RawMessage(tc.value)}
   raw,err:=json.Marshal(fields);if err!=nil{t.Fatal(err)}
   value,err:=NewRequestFixture(raw);if (err==nil)!=tc.valid{t.Fatalf("valid=%v error=%v",tc.valid,err)};if err!=nil{return}
   out,err:=ethcodec.MarshalNoEscape(value);if err!=nil{t.Fatal(err)}
   equal,err:=ethcodec.JSONValuesEqual(raw,out);if err!=nil||!equal{t.Fatalf("request content changed: %s -> %s (%v)",raw,out,err)}
  })
 }
 for _,raw:=range []string{
  "{\"choice\":\"yes\",\"choice\":\"no\"}",
  "{\"choice\":\"yes\",\"rows\":[{}],\"dictionary\":{},\"closed\":{},\"left\":\"a\",\"right\":\"b\"}",
  "{\"choice\":\"yes\",\"rows\":[{}],\"dictionary\":{},\"closed\":{},\"unknown\":{\"x\":1,\"x\":2}}",
 } {if _,err:=NewRequestFixture([]byte(raw));err==nil{t.Fatalf("unsafe/forbidden request accepted: %s",raw)}}
 value,err:=NewRequestFixture([]byte(baseline));if err!=nil{t.Fatal(err)}
 for _,extras:=range []map[string]json.RawMessage{
  {"choice":json.RawMessage("\"yes\"")},
  {"left":json.RawMessage("\"x\"")},
  {"extra":json.RawMessage("invalid")},
  {"extra":json.RawMessage("{\"x\":1,\"x\":2}")},
 } {value.AdditionalProperties=extras;if _,err:=json.Marshal(value);err==nil{t.Fatalf("invalid extras encoded: %+v",extras)}}
 raw:=[]byte("{\"choice\":\"yes\",\"rows\":[{}],\"dictionary\":{},\"closed\":{},\"LEFT\":\"unknown case\"}")
 value,err=NewRequestFixture(raw);if err!=nil{t.Fatal(err)};if value.Left!=nil||string(value.AdditionalProperties["LEFT"])!="\"unknown case\""{t.Fatal("case-insensitive decoding changed an unknown member")}
}
`

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

func TestValueValidationSelectionRejectsUnsupportedGraphs(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		request         bool
	}{
		{"unique", `{"type":"array","uniqueItems":true,"items":{"type":"string"}}`, "uniqueItems", false},
		{"union", `{"oneOf":[{"type":"string"},{"type":"boolean"}]}`, "multi-branch", false},
		{"number", `{"type":"number"}`, "non-string scalar Number", false},
		{"boolean", `{"type":"boolean"}`, "non-string scalar bool", false},
		{"integer", `{"type":"integer","const":1}`, "non-string scalar int", false},
		{"dictionary", `{"type":"object","patternProperties":{"^x$":{"type":"string"}},"additionalProperties":false}`, "dictionary", false},
		{"retained", `{"type":"object","properties":{"x":{"type":"string"}}}`, "retained additional properties", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := newRegistry(executionapi.Manifest{ValueValidationResults: []string{"eth_selected"}}, &genReport{visited: map[string]bool{}})
			reg.strictRequests = tc.request
			r, err := reg.walk(testSchema(t, `{"type":"object","properties":{"child":`+tc.raw+`}}`), "eth_selected.result")
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "generated.go")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			roots := map[string]*ref{"eth_selected": r}
			err = emit(reg, "fixture", roots, target)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "eth_selected.result") {
				t.Fatalf("unsupported selection: %v", err)
			}
			if output, err := os.ReadFile(target); err != nil || string(output) != "unchanged" {
				t.Fatalf("failed emission changed output: %q %v", output, err)
			}
			// The opt-in restriction must not narrow ordinary constructors.
			reg.manifest.ValueValidationResults = nil
			if err := emit(reg, "fixture", roots, target); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("missing-root", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{ValueValidationResults: []string{"eth_missing"}}, &genReport{})
		if _, err := reg.valueValidationObjects(nil); err == nil || !strings.Contains(err.Error(), "eth_missing.result") {
			t.Fatal(err)
		}
	})
	t.Run("public-collision", func(t *testing.T) {
		reg := newRegistry(executionapi.Manifest{ValueValidationResults: []string{"eth_collision"}}, &genReport{})
		reg.objects = []*objectModel{{name: methodValidatorName("eth_collision")}}
		if err := reg.validatePublicNames(map[string]*ref{"eth_collision": {native: "Text"}}); err == nil {
			t.Fatal("accepted public validator collision")
		}
	})
}

func TestGeneratedValueValidationPreservesEncodedSemantics(t *testing.T) {
	reg := newRegistry(executionapi.Manifest{ValueValidationResults: []string{"eth_fixture"}}, &genReport{visited: map[string]bool{}})
	r, err := reg.walk(testSchema(t, `{
		"type":"object","title":"Value fixture","additionalProperties":false,
		"required":["required","nullableRows","rows"],"not":{"required":["left","right"]},
		"properties":{
			"required":{"type":"string"},
			"nullableRows":{"anyOf":[{"type":"null"},{"type":"array","minItems":1,"items":{"type":"string"}}]},
			"optionalRows":{"anyOf":[{"type":"null"},{"type":"array","minItems":1,"items":{"type":"string"}}]},
			"rows":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"string","pattern":"^ok$"}},
			"choice":{"type":"string","enum":["yes","��",null,1]},
			"literal":{"type":"string","const":"��"},
			"pattern":{"type":"string","pattern":"^��$"},
			"addressConst":{"type":"string","pattern":"^0x[0-9a-fA-F]{40}$","const":"0xabcdef0000000000000000000000000000000000"},
			"addressEnum":{"type":"string","pattern":"^0x[0-9a-fA-F]{40}$","enum":["0xabcdef0000000000000000000000000000000000"]},
			"byteConst":{"type":"string","pattern":"^0x[0-9a-f]{1,2}$","const":"0x1"},
			"byteEnum":{"type":"string","pattern":"^0x[0-9a-f]{1,2}$","enum":["0x1"]},
			"byteNative":{"type":"string","pattern":"^0x[0-9a-f]{1,2}$"},
			"left":{"type":"string"},"right":{"type":"string"}
		}
	}`), "eth_fixture.result")
	if err != nil {
		t.Fatal(err)
	}
	// Literal constraints deliberately select Text, even when the pattern
	// alone would select a native that normalizes its representation. Both
	// parser and value validator must compare the original string identity.
	for _, field := range reg.objects[0].fields {
		switch field.json {
		case "addressConst", "addressEnum", "byteConst", "byteEnum":
			if field.ref.native != "Text" {
				t.Fatalf("%s lost literal identity: %s", field.json, field.ref.native)
			}
		case "byteNative":
			if field.ref.native != "HexByte" {
				t.Fatalf("unconstrained native changed: %s", field.ref.native)
			}
		}
	}
	runValueGeneratedTests(t, reg, map[string]*ref{"eth_fixture": r}, valueEncodedSemanticsTest, false)
}

// Compile real generated models together with the unchanged production
// accesslist preparation code. A disconnected validator can pass synthetic
// generator tests while the runtime still bypasses a newly added constraint.
func TestAccessListValueValidationSchemaMutationsReachConsumer(t *testing.T) {
	bundle, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, pointer, value, input string
		valid                       bool
	}{
		{"baseline", "", "", `{"ACCESSLIST":[],"vendor":1,"error":null,"gasUsed":null}`, true},
		{"root-not", "not", `{"required":["accessList","gasUsed"]}`, `{"accessList":[],"gasUsed":"0x1"}`, false},
		{"array-max", "properties/accessList/maxItems", `0`, `{"accessList":[{"address":"0x0000000000000000000000000000000000000001","storageKeys":[]}]}`, false},
		{"nested-max", "properties/accessList/items/properties/storageKeys/maxItems", `0`, `{"accessList":[{"address":"0x0000000000000000000000000000000000000001","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001"]}]}`, false},
		{"field-pattern", "properties/error/pattern", `"^allowed$"`, `{"accessList":[],"error":"blocked"}`, false},
		{"field-const", "properties/error/const", `"allowed"`, `{"accessList":[],"error":"blocked"}`, false},
		{"field-enum", "properties/error/enum", `["allowed"]`, `{"accessList":[],"error":"blocked"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(bundle.Results["eth_createAccessList"], &schema); err != nil {
				t.Fatal(err)
			}
			if tc.pointer != "" {
				parts := strings.Split(tc.pointer, "/")
				node := schema
				for _, part := range parts[:len(parts)-1] {
					node = node[part].(map[string]any)
				}
				var value any
				if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
					t.Fatal(err)
				}
				node[parts[len(parts)-1]] = value
			}
			raw, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			reg := testRegistry()
			reg.manifest.Aliases = nil
			r, err := reg.walk(testSchema(t, string(raw)), "eth_createAccessList.result")
			if err != nil {
				t.Fatal(err)
			}
			runValueGeneratedTests(t, reg, map[string]*ref{"eth_createAccessList": r}, fmt.Sprintf(valueConsumerTest, tc.input, tc.valid), true)
		})
	}
}

func TestSelectedValueValidatorAudit(t *testing.T) {
	bundle, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	reg, roots, err := compileBundle(bundle, testManifest())
	if err != nil {
		t.Fatal(err)
	}
	audit, err := buildGenerationAudit(bundle, reg, roots)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range audit.Results {
		if entry.ValueValidator == "" {
			continue
		}
		count++
		if entry.Method != "eth_createAccessList" || entry.ValueValidator != "ValidateCreateAccessListResult" {
			t.Fatalf("unexpected validator trace: %+v", entry)
		}
	}
	if count != 1 {
		t.Fatalf("validator audit count = %d", count)
	}
}

func TestAccessListRequiredMutationChangesValueAndConsumerContract(t *testing.T) {
	bundle, err := executionapi.Read()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(bundle.Results["eth_createAccessList"], &schema); err != nil {
		t.Fatal(err)
	}
	schema["required"] = []string{"gasUsed"}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	reg := testRegistry()
	reg.manifest.Aliases = nil
	r, err := reg.walk(testSchema(t, string(raw)), "eth_createAccessList.result")
	if err != nil {
		t.Fatal(err)
	}
	dir := runValueGeneratedTests(t, reg, map[string]*ref{"eth_createAccessList": r}, valueRequiredMutationTest, false)
	// Making a field required changes its Go shape, so the old preparation
	// cannot silently keep omitting it. It must be updated consciously.
	consumer, err := os.ReadFile("../ethobject/accesslist/encode.go")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "internal/ethobject/accesslist")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "encode.go"), consumer, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "./internal/ethobject/accesslist")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off")
	output, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("cannot use &gas")) || !bytes.Contains(output, []byte("ethapi.Quantity")) {
		t.Fatalf("required field did not guard consumer migration: %v\n%s", err, output)
	}
}

func runValueGeneratedTests(t *testing.T, reg *registry, roots map[string]*ref, tests string, consumer bool) string {
	t.Helper()
	dir := t.TempDir()
	write := func(path string, raw []byte) {
		t.Helper()
		target := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", []byte("module github.com/codermaybe/ethguard\n\ngo 1.26.10\n"))
	write("internal/spec/spec.go", []byte("package spec\nfunc Version() string { return \"value-test\" }\n"))
	write("ethapi/generated_test.go", []byte(tests))
	files := []string{"jsonsafe/jsonsafe.go", "limits/limits.go"}
	for _, file := range []string{"helpers.go", "normalize.go", "strict.go", "compatibility.go", "numbers.go", "number.go", "equality.go", "fixed_hex.go", "jsonstring.go", "spelling.go"} {
		files = append(files, "ethcodec/"+file)
	}
	if consumer {
		files = append(files, "ethobject/accesslist/encode.go")
	}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join("..", file))
		if err != nil {
			t.Fatal(err)
		}
		write("internal/"+file, raw)
	}
	target := filepath.Join(dir, "ethapi/types.gen.go")
	if err := emit(reg, "value-test", roots, target); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := emit(reg, "value-test", roots, target); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("value emission is nondeterministic", err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated value contract: %v\n%s", err, output)
	}
	return dir
}

const valueEncodedSemanticsTest = `package ethapi_test
import (
 "encoding/json"
 "reflect"
 "testing"
 "github.com/codermaybe/ethguard/ethapi"
)
func ptr[T any](v T) *T { return &v }
func TestEncodedSemantics(t *testing.T) {
 for _, tc := range []struct { name string; change func(*ethapi.ValueFixture); valid bool }{
  {"required-zero", func(*ethapi.ValueFixture){}, true},
  {"null-pointer",func(v *ethapi.ValueFixture){v.NullableRows=nil},true},
  {"nil-slice-pointer",func(v *ethapi.ValueFixture){v.NullableRows=ptr([]ethapi.Text(nil))},true},
  {"empty-nullable-slice",func(v *ethapi.ValueFixture){v.NullableRows=ptr([]ethapi.Text{})},false},
  {"optional-null-pointer",func(v *ethapi.ValueFixture){v.OptionalRows=ptr((*[]ethapi.Text)(nil))},true},
  {"optional-nil-slice",func(v *ethapi.ValueFixture){v.OptionalRows=ptr(ptr([]ethapi.Text(nil)))},true},
  {"optional-empty-slice",func(v *ethapi.ValueFixture){v.OptionalRows=ptr(ptr([]ethapi.Text{}))},false},
  {"nil-required-array",func(v *ethapi.ValueFixture){v.Rows=nil},false},
  {"min-items",func(v *ethapi.ValueFixture){v.Rows=[]ethapi.Text{}},false},
  {"max-items",func(v *ethapi.ValueFixture){v.Rows=[]ethapi.Text{"ok","ok","ok"}},false},
  {"array-item-pattern",func(v *ethapi.ValueFixture){v.Rows=[]ethapi.Text{"bad"}},false},
  {"enum",func(v *ethapi.ValueFixture){v.Choice=ptr(ethapi.Text("no"))},false},
  {"enum-non-string",func(v *ethapi.ValueFixture){v.Choice=ptr(ethapi.Text("1"))},false},
  {"invalid-utf8-enum",func(v *ethapi.ValueFixture){v.Choice=ptr(ethapi.Text("\xff\xfe"))},true},
  {"invalid-utf8-const",func(v *ethapi.ValueFixture){v.Literal=ptr(ethapi.Text("\xff\xfe"))},true},
  {"invalid-utf8-pattern",func(v *ethapi.ValueFixture){v.Pattern=ptr(ethapi.Text("\xff\xfe"))},true},
  {"const",func(v *ethapi.ValueFixture){v.Literal=ptr(ethapi.Text("�"))},false},
  {"pattern",func(v *ethapi.ValueFixture){v.Pattern=ptr(ethapi.Text("�"))},false},
  {"address-const-case",func(v *ethapi.ValueFixture){v.AddressConst=ptr(ethapi.Text("0xABCDEF0000000000000000000000000000000000"))},false},
  {"address-const-exact",func(v *ethapi.ValueFixture){v.AddressConst=ptr(ethapi.Text("0xabcdef0000000000000000000000000000000000"))},true},
  {"address-enum-case",func(v *ethapi.ValueFixture){v.AddressEnum=ptr(ethapi.Text("0xABCDEF0000000000000000000000000000000000"))},false},
  {"address-enum-exact",func(v *ethapi.ValueFixture){v.AddressEnum=ptr(ethapi.Text("0xabcdef0000000000000000000000000000000000"))},true},
  {"byte-const-width",func(v *ethapi.ValueFixture){v.ByteConst=ptr(ethapi.Text("0x01"))},false},
  {"byte-const-exact",func(v *ethapi.ValueFixture){v.ByteConst=ptr(ethapi.Text("0x1"))},true},
  {"byte-enum-width",func(v *ethapi.ValueFixture){v.ByteEnum=ptr(ethapi.Text("0x01"))},false},
  {"byte-enum-exact",func(v *ethapi.ValueFixture){v.ByteEnum=ptr(ethapi.Text("0x1"))},true},
  {"byte-native-width",func(v *ethapi.ValueFixture){v.ByteNative=ptr(ethapi.HexByte("0x01"))},true},
  {"one-present",func(v *ethapi.ValueFixture){v.Left=ptr(ethapi.Text(""))},true},
  {"forbidden",func(v *ethapi.ValueFixture){v.Left=ptr(ethapi.Text(""));v.Right=ptr(ethapi.Text(""))},false},
 } {
  t.Run(tc.name,func(t *testing.T){
   v:=ethapi.ValueFixture{Rows:[]ethapi.Text{"ok"},NullableRows:ptr([]ethapi.Text{"row"})}
   tc.change(&v)
   before:=[]string{}
   for _,s:=range []*ethapi.Text{v.Literal,v.Choice,v.Pattern,v.AddressConst,v.AddressEnum,v.ByteConst,v.ByteEnum}{ if s!=nil{before=append(before,string(*s))} }
   if v.ByteNative!=nil{before=append(before,string(*v.ByteNative))}
   raw,err:=v.MarshalJSON();if err!=nil{t.Fatal(err)}
   _,parseErr:=ethapi.NewFixtureResult(raw)
   valueErr:=ethapi.ValidateFixtureResult(v)
   if (parseErr==nil)!=tc.valid || (valueErr==nil)!=tc.valid { t.Fatalf("raw=%s parse=%v value=%v want-valid=%v",raw,parseErr,valueErr,tc.valid) }
   after:=[]string{}
   for _,s:=range []*ethapi.Text{v.Literal,v.Choice,v.Pattern,v.AddressConst,v.AddressEnum,v.ByteConst,v.ByteEnum}{ if s!=nil{after=append(after,string(*s))} }
   if v.ByteNative!=nil{after=append(after,string(*v.ByteNative))}
   if !reflect.DeepEqual(before,after){t.Fatal("validation changed string values")}
  })
 }
 // Required Go zero values are present when marshaled; missing JSON members
 // remain a separate constructor check that a value cannot reconstruct.
 if _,err:=ethapi.NewFixtureResult(json.RawMessage("{\"rows\":[\"ok\"],\"nullableRows\":null}"));err==nil{t.Fatal("missing required JSON accepted")}
}
`

const valueConsumerTest = `package ethapi_test
import (
 "encoding/json"
 "testing"
 "github.com/codermaybe/ethguard/ethapi"
 "github.com/codermaybe/ethguard/internal/ethobject/accesslist"
)
func TestConsumer(t *testing.T) {
 raw:=json.RawMessage(%q)
 valid:=%t
 // Parse with encoding/json solely to construct a direct Go value that has
 // not crossed the generated method constructor.
 var v ethapi.AccessListResult
 if err:=json.Unmarshal(raw,&v);err!=nil{t.Fatal(err)}
 canonical,err:=v.MarshalJSON();if err!=nil{t.Fatal(err)}
 _,constructorErr:=ethapi.NewCreateAccessListResult(canonical)
 valueErr:=ethapi.ValidateCreateAccessListResult(v)
 _,consumerErr:=accesslist.Encode(raw)
 if (constructorErr==nil)!=valid || (valueErr==nil)!=valid || (consumerErr==nil)!=valid{
  t.Fatalf("contract diverged: constructor=%%v value=%%v consumer=%%v expected=%%v",constructorErr,valueErr,consumerErr,valid)
 }
}
`

const valueRequiredMutationTest = `package ethapi_test
import (
 "encoding/json"
 "testing"
 "github.com/codermaybe/ethguard/ethapi"
)
func TestRequiredValue(t *testing.T) {
 for _, tc:=range []struct{quantity ethapi.Quantity; valid bool}{{"",false},{"0x0",true},{"0x01",false}} {
  v:=ethapi.AccessListResult{GasUsed:tc.quantity}
  raw,err:=v.MarshalJSON();if err!=nil{t.Fatal(err)}
  _,parseErr:=ethapi.NewCreateAccessListResult(raw)
  valueErr:=ethapi.ValidateCreateAccessListResult(v)
  if (parseErr==nil)!=tc.valid || (valueErr==nil)!=tc.valid{t.Fatalf("constructor=%v value=%v want-valid=%v",parseErr,valueErr,tc.valid)}
 }
 if _,err:=ethapi.NewCreateAccessListResult(json.RawMessage("{}"));err==nil{t.Fatal("new required member was ignored")}
}
`

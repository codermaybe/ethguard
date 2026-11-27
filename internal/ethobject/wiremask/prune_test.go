package wiremask

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPrunePreservesPresenceAndArrayPositions(t *testing.T) {
	leaf := &MaskNode{Wildcard: true}
	mask := &MaskNode{Properties: map[string]*MaskNode{
		"present": leaf,
		"missing": leaf,
		"items":   {Items: &MaskNode{Properties: map[string]*MaskNode{"value": leaf}}},
	}}
	data := map[string]any{
		"present": nil,
		"vendor":  "ignored",
		"items": []any{
			nil,
			map[string]any{"value": nil, "vendor": "ignored"},
			map[string]any{"vendor": "ignored"},
			false,
			json.Number("9007199254740993"),
			"tail",
			nil,
		},
	}
	want := map[string]any{
		"present": nil,
		"items": []any{
			nil, map[string]any{"value": nil}, map[string]any{},
			false, json.Number("9007199254740993"), "tail", nil,
		},
	}
	if got := Prune(data, mask); !reflect.DeepEqual(got, want) {
		t.Fatalf("Prune = %#v, want %#v", got, want)
	}
}

func TestSimulateMaskUsesTheSharedOfficialBundle(t *testing.T) {
	const raw = `[{"number":"0x1","vendor":"block","calls":[{"status":"0x1","returnData":"0x00","gasUsed":"0x0","logs":[{"address":"0x0000000000000000000000000000000000000001","vendor":"log"}],"vendor":"call"}]}]`
	const want = `[{"calls":[{"gasUsed":"0x0","logs":[{"address":"0x0000000000000000000000000000000000000001"}],"returnData":"0x00","status":"0x1"}],"number":"0x1"}]`
	got, err := PruneMethod("eth_simulateV1", json.RawMessage(raw))
	if err != nil || got != want {
		t.Fatalf("shared simulate mask = %s, %v; want %s", got, err, want)
	}
}

func TestPruneMethodNumberFidelity(t *testing.T) {
	for _, number := range []string{
		"9007199254740992", "9007199254740993",
		"18446744073709551615", "-9007199254740993",
		"0.1234567890123456789012345678901", "1e400", "1e-400",
	} {
		t.Run(number, func(t *testing.T) {
			for _, tc := range []struct {
				method string
				raw    string
			}{
				{"eth_blockNumber", number},
				{"eth_feeHistory", `{"gasUsedRatio":[` + number + `]}`},
			} {
				got, err := PruneMethod(tc.method, json.RawMessage(tc.raw))
				if err != nil || got != tc.raw {
					t.Fatalf("%s: got %q, %v; want %s", tc.method, got, err, tc.raw)
				}
			}
		})
	}
}

func TestPruneMethodNullAndMissing(t *testing.T) {
	const method = "eth_getTransactionByHash"
	for _, tc := range []struct{ raw, want string }{
		{`null`, `null`},
		{`{}`, `{}`},
		{`{"to":null}`, `{"to":null}`},
		{`{"accessList":null,"vendor":null}`, `{"accessList":null}`},
		{`{"accessList":[]}`, `{"accessList":[]}`},
		{`{"accessList":[null]}`, `{"accessList":[null]}`},
		{`{"accessList":[{}]}`, `{"accessList":[{}]}`},
	} {
		t.Run(method+"/"+tc.raw, func(t *testing.T) {
			got, err := PruneMethod(method, json.RawMessage(tc.raw))
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestPruneMethodNestedVendorIsolation(t *testing.T) {
	const official = `{"accessList":[null,{"address":null,"storageKeys":[null,"0x00ab",null]},{}],"authorizationList":[{"address":"0xab","nonce":null},null]}`
	const withVendor = `{
		"vendor":{"to":"injected","deep":[null,9007199254740993]},
		"accessList":[null,{
			"address":null,"storageKeys":[null,"0X00AB",null],
			"vendor":{"address":"injected","storageKeys":["injected"]}
		},{"vendor":null}],
		"authorizationList":[{"address":"0XAB","nonce":null,"vendor":[{"nonce":"injected"}]},null]
	}`
	const method = "eth_getTransactionByHash"
	for _, raw := range []string{official, withVendor} {
		got, err := PruneMethod(method, json.RawMessage(raw))
		if err != nil || got != official {
			t.Fatalf("%s: got %q, %v; want %s", method, got, err, official)
		}
	}
	for _, raw := range []string{
		`{"accessList":[null,{"storageKeys":[null,"0x00ac",null]}]}`,
		`{"accessList":[{"storageKeys":["0x00ab",null,null]},null]}`,
	} {
		got, err := PruneMethod("eth_getTransactionByHash", json.RawMessage(raw))
		if err != nil || got != raw {
			t.Fatalf("official array changed: got %q, %v; want %s", got, err, raw)
		}
	}
}

func TestPruneMethodNestedArrays(t *testing.T) {
	const raw = `{"gasUsedRatio":[null,0,0.123456789012345678901,false,null],"reward":[null,[],[null,"0x00ab",null],null]}`
	got, err := PruneMethod("eth_feeHistory", json.RawMessage(raw))
	if err != nil || got != raw {
		t.Fatalf("got %q, %v; want %s", got, err, raw)
	}
}

func TestPruneMethodRejectsInvalidJSON(t *testing.T) {
	for _, raw := range []string{"", " ", "null false", "{} {}", "{} junk", "[null,]", "01"} {
		t.Run(raw, func(t *testing.T) {
			if key, err := PruneMethod("eth_getTransactionByHash", json.RawMessage(raw)); err == nil {
				t.Fatalf("invalid JSON accepted as %s", key)
			}
		})
	}
	got, err := PruneMethod("eth_getTransactionByHash", json.RawMessage(" \t\r\nnull \t\r\n"))
	if err != nil || got != "null" {
		t.Fatalf("JSON whitespace: got %q, %v", got, err)
	}
}

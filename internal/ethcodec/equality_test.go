package ethcodec_test

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
)

func TestJSONValuesEqualUsesRecursiveExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		equal       bool
	}{
		{`{"a":[1,null,{"x":2}],"b":true}`, `{"b":true,"a":[1e0,null,{"x":2.0}]}`, true},
		{`9007199254740992`, `9007199254740993`, false},
		{`1.0000000000000000000001`, `1.000000000000000000000100`, true},
		{`1.0000000000000000000001`, `1.0000000000000000000002`, false},
		{`1e-9999999999999999999999999999`, `10e-10000000000000000000000000000`, true},
		{`[null]`, `[]`, false}, {`{"x":null}`, `{}`, false}, {`1`, `"1"`, false},
		{`[1,2]`, `[2,1]`, false},
	} {
		equal, err := ethcodec.JSONValuesEqual(json.RawMessage(tc.left), json.RawMessage(tc.right))
		if err != nil || equal != tc.equal {
			t.Errorf("%s / %s: equal=%v err=%v", tc.left, tc.right, equal, err)
		}
	}
	for _, raw := range []string{`invalid`, `1 2`, `{"x":`} {
		if _, err := ethcodec.JSONValuesEqual(json.RawMessage(raw), json.RawMessage(`1`)); err == nil {
			t.Errorf("invalid JSON accepted: %s", raw)
		}
	}
}

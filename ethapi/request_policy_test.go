package ethapi_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

const callPolicyAuthorization = `{"address":"0x0000000000000000000000000000000000000001","chainId":"0x1","nonce":"0x0","r":"0x1","s":"0x2","yParity":"0x0","vendor":{"integer":900719925474099312345678901234567890,"nullable":null},"optional":null,"ADDRESS":"unknown"}`

func TestGeneratedCallRequestRejectsUnknownClosedMembers(t *testing.T) {
	for _, raw := range []string{
		`{"unknown":true}`, `{"TO":null}`,
		`{"accessList":[{"address":"0x0000000000000000000000000000000000000001","storageKeys":[],"unknown":1}]}`,
		`{"accessList":[{"ADDRESS":"0x0000000000000000000000000000000000000001","storageKeys":[]}]}`,
		`{"authorizationList":[{"address":"0x0000000000000000000000000000000000000001","chainId":"0x1","r":"0x1","s":"0x2","yParity":"0x0"}]}`,
		`{"to":null,"to":null}`,
		`{"authorizationList":[{"address":"0x0000000000000000000000000000000000000001","chainId":"0x1","nonce":"0x0","r":"0x1","s":"0x2","yParity":"0x0","extra":{"x":1,"x":2}}]}`,
		`{"to":null} {}`, `null`, `[]`,
	} {
		if _, err := ethapi.NewCallMsg(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid request admitted: %s", raw)
		}
	}
}

func TestGeneratedCallRequestPreservesOpenAuthorizationAndPresence(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"to":null}`, `{"to":"0x0000000000000000000000000000000000000001"}`,
		`{"authorizationList":[` + callPolicyAuthorization + `],"accessList":[],"commitments":[],"proofs":[],"to":null}`,
	} {
		value, err := ethapi.NewCallMsg(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decode := func(raw []byte) any {
			t.Helper()
			var result any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		if !reflect.DeepEqual(decode([]byte(raw)), decode(encoded)) {
			t.Fatalf("request content/presence changed: %s -> %s", raw, encoded)
		}
		if value.AuthorizationList != nil {
			extras := (*value.AuthorizationList)[0].AdditionalProperties
			if len(extras) != 3 || string(extras["optional"]) != "null" || string(extras["ADDRESS"]) != `"unknown"` || string(extras["vendor"]) != `{"integer":900719925474099312345678901234567890,"nullable":null}` {
				t.Fatalf("open authorization members were dropped, rounded, or case-folded: %+v", extras)
			}
		}
	}
}

func TestGeneratedCallRequestMarshalRejectsUnsafeAuthorizationExtras(t *testing.T) {
	for _, extras := range []map[string]json.RawMessage{
		{"address": json.RawMessage(`"0x0000000000000000000000000000000000000001"`)},
		{"nonce": json.RawMessage(`"0x0"`)},
		{"extra": json.RawMessage(`invalid`)},
		{"extra": json.RawMessage(`{"x":1,"x":2}`)},
		{"extra": nil},
	} {
		value, err := ethapi.NewCallMsg(json.RawMessage(`{"authorizationList":[` + callPolicyAuthorization + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		(*value.AuthorizationList)[0].AdditionalProperties = extras
		if encoded, err := json.Marshal(value); err == nil {
			t.Errorf("unsafe/colliding extras encoded: %s", encoded)
		}
	}
}

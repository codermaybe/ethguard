package rpcwire

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestDecodeSubscriptionNotification(t *testing.T) {
	for _, tc := range []struct {
		name, body, result string
	}{
		{"object", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":{"number":"0x1"}}}`, `{"number":"0x1"}`},
		{"null", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":null}}`, `null`},
		{"number", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":0}}`, `0`},
		{"boolean", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":false}}`, `false`},
		{"empty_string", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":""}}`, `""`},
		{"array", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":[null,1]}}`, `[null,1]`},
		{"unknown_members", `{"jsonrpc":"2.0","method":"eth_subscription","extra":true,"params":{"subscription":"sub-1","extra":[],"result":{}}}`, `{}`},
		{"payload_is_not_response", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":{"error":null,"result":1}}}`, `{"error":null,"result":1}`},
		{"member_matching_unchanged", `{"JSONRPC":"2.0","METHOD":"eth_subscription","PARAMS":{"SUBSCRIPTION":"sub-1","RESULT":1}}`, `1`},
		{"duplicate_members_unchanged", `{"jsonrpc":"1.0","jsonrpc":"2.0","method":"other","method":"eth_subscription","params":{"subscription":"old","subscription":"sub-1","result":0,"result":1}}`, `1`},
		{"escaped_values", `{"jsonrpc":"2.0","method":"eth_\u0073ubscription","params":{"subscription":"sub-\u0031","result":"\u0041"}}`, `"\u0041"`},
		{"escaped_version", `{"jsonrpc":"2.\u0030","method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`, `1`},
		{"whitespace", " \n {\"jsonrpc\" : \"2.0\", \"method\": \"eth_subscription\", \"params\" : {\"subscription\": \"sub-1\", \"result\" : { \"a\" : 1 }}} \t ", `{ "a" : 1 }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := DecodeSubscriptionNotification([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if n.Method != "eth_subscription" || n.SubscriptionID != "sub-1" || string(n.Result) != tc.result {
				t.Fatalf("notification = %+v, result = %s; want sub-1 and %s", n, n.Result, tc.result)
			}
		})
	}
}

func TestDecodeSubscriptionNotificationRejectsMalformed(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ``},
		{"whitespace", " \n\t "},
		{"invalid_json", `{"jsonrpc":"2.0",`},
		{"trailing_garbage", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":1}} garbage`},
		{"second_value", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":1}} {}`},
		{"batch", `[{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":1}}]`},
		{"null_body", `null`},
		{"string_body", `"notification"`},
		{"number_body", `1`},
		{"boolean_body", `true`},
		{"missing_version", `{"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"wrong_version", `{"jsonrpc":"1.0","method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"null_version", `{"jsonrpc":null,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"numeric_version", `{"jsonrpc":2.0,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"boolean_version", `{"jsonrpc":true,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"object_version", `{"jsonrpc":{},"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"array_version", `{"jsonrpc":["2.0"],"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"duplicate_version_last_null", `{"jsonrpc":"2.0","jsonrpc":null,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"duplicate_version_last_wrong", `{"jsonrpc":"2.0","jsonrpc":"1.0","method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"duplicate_version_last_number", `{"jsonrpc":"2.0","jsonrpc":2.0,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"missing_method", `{"jsonrpc":"2.0","params":{"subscription":"sub-1","result":1}}`},
		{"wrong_method", `{"jsonrpc":"2.0","method":"other","params":{"subscription":"sub-1","result":1}}`},
		{"null_method", `{"jsonrpc":"2.0","method":null,"params":{"subscription":"sub-1","result":1}}`},
		{"numeric_method", `{"jsonrpc":"2.0","method":1,"params":{"subscription":"sub-1","result":1}}`},
		{"null_id", `{"jsonrpc":"2.0","id":null,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"numeric_id", `{"jsonrpc":"2.0","id":1,"method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"string_id", `{"jsonrpc":"2.0","id":"one","method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`},
		{"missing_params", `{"jsonrpc":"2.0","method":"eth_subscription"}`},
		{"null_params", `{"jsonrpc":"2.0","method":"eth_subscription","params":null}`},
		{"array_params", `{"jsonrpc":"2.0","method":"eth_subscription","params":["sub-1",1]}`},
		{"string_params", `{"jsonrpc":"2.0","method":"eth_subscription","params":"sub-1"}`},
		{"missing_subscription", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"result":1}}`},
		{"null_subscription", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":null,"result":1}}`},
		{"empty_subscription", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"","result":1}}`},
		{"numeric_subscription", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":1,"result":1}}`},
		{"missing_result", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1"}}`},
		{"params_error_cannot_replace_result", `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","error":{"code":3,"message":"reverted"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := DecodeSubscriptionNotification([]byte(tc.body))
			if !errors.Is(err, ErrMalformedNotification) {
				t.Fatalf("error = %v, want ErrMalformedNotification", err)
			}
			if n.Method != "" || n.SubscriptionID != "" || n.Result != nil {
				t.Fatalf("invalid notification returned data: %+v", n)
			}
			var protocol rpc.ProtocolError
			var data rpc.DataError
			var presence rpc.ErrorDataPresence
			if errors.As(err, &protocol) || errors.As(err, &data) || errors.As(err, &presence) {
				t.Fatalf("wire validation error exposes provider error capabilities: %v", err)
			}
		})
	}
}

func TestSubscriptionNotificationOwnsResult(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":{"number":"0x1"}}}`)
	n, err := DecodeSubscriptionNotification(body)
	if err != nil {
		t.Fatal(err)
	}
	for i := range body {
		body[i] = 'x'
	}
	if n.Method != "eth_subscription" || n.SubscriptionID != "sub-1" || string(n.Result) != `{"number":"0x1"}` {
		t.Fatalf("notification changed after body reuse: %+v, result = %s", n, n.Result)
	}
}

func FuzzDecodeSubscriptionNotification(f *testing.F) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":null}}`,
		`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1","result":{"number":"0x1"}}}`,
		`{"jsonrpc":"1.0","method":"eth_subscription","params":{"subscription":"sub-1","result":1}}`,
		`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"sub-1"}}`,
		`{"jsonrpc":"2.0","id":1,"result":1}`, `null`, `[]`, ``,
	} {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		n, err := DecodeSubscriptionNotification(body)
		if err != nil {
			if !errors.Is(err, ErrMalformedNotification) {
				t.Fatalf("unexpected error classification: %v", err)
			}
			if n.Method != "" || n.SubscriptionID != "" || n.Result != nil {
				t.Fatalf("error returned alongside notification: %+v", n)
			}
			return
		}
		if n.Method != "eth_subscription" || n.SubscriptionID == "" || !json.Valid(n.Result) {
			t.Fatalf("accepted invalid notification: %+v", n)
		}
	})
}

package rpcwire

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The tests in this file are the frozen contract of the project-owned
// JSON-RPC codec (docs/design/http-transport-differential.md, "EthGuard
// 自有 HTTP contract"). Deliberate differences from the Geth v1.17.5
// baseline are called out inline and mirrored in the differential test
// suite of internal/transport/http.

func TestEncodeRequestEnvelope(t *testing.T) {
	cases := []struct {
		name    string
		id      json.RawMessage
		method  string
		params  []any
		want    string
		wantErr bool
	}{
		{
			name:   "no-args-omits-params",
			id:     json.RawMessage(`1`),
			method: "eth_chainId",
			want:   `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
		},
		{
			name:   "empty-non-nil-slice-sends-empty-array",
			id:     json.RawMessage(`2`),
			method: "eth_chainId",
			params: []any{},
			want:   `{"jsonrpc":"2.0","id":2,"method":"eth_chainId","params":[]}`,
		},
		{
			name:   "args-become-ordered-array",
			id:     json.RawMessage(`3`),
			method: "eth_getBalance",
			params: []any{"0x1", "latest"},
			want:   `{"jsonrpc":"2.0","id":3,"method":"eth_getBalance","params":["0x1","latest"]}`,
		},
		{
			name:   "string-id",
			id:     json.RawMessage(`"abc"`),
			method: "eth_chainId",
			want:   `{"jsonrpc":"2.0","id":"abc","method":"eth_chainId"}`,
		},
		{
			name:    "empty-method-rejected",
			id:      json.RawMessage(`1`),
			method:  "",
			wantErr: true,
		},
		{
			name:    "null-id-rejected",
			id:      json.RawMessage(`null`),
			method:  "eth_chainId",
			wantErr: true,
		},
		{
			name:    "object-id-rejected",
			id:      json.RawMessage(`{"a":1}`),
			method:  "eth_chainId",
			wantErr: true,
		},
		{
			name:    "empty-id-rejected",
			id:      nil,
			method:  "eth_chainId",
			wantErr: true,
		},
		{
			name:    "unmarshalable-param-rejected",
			id:      json.RawMessage(`1`),
			method:  "eth_chainId",
			params:  []any{make(chan int)},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EncodeRequest(tc.id, tc.method, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("EncodeRequest = %s, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("EncodeRequest err = %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("wire = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestIDGenUniqueConcurrencySafe(t *testing.T) {
	g := NewIDGen()
	first, ok := parseID(g.Next())
	if !ok {
		t.Fatal("generated id is not a valid JSON-RPC id")
	}
	if first.(json.Number).String() != "1" {
		t.Fatalf("first id = %s, want 1", first)
	}
	const concurrency = 32
	const perGoroutine = 25
	seen := make([]string, concurrency*perGoroutine)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				seen[i*perGoroutine+j] = string(g.Next())
			}
		}(i)
	}
	wg.Wait()
	set := map[string]bool{}
	for _, id := range seen {
		if set[id] {
			t.Fatalf("duplicate id %s under concurrency", id)
		}
		set[id] = true
	}
}

func TestDecodeResponseSuccessForms(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		requestID json.RawMessage
		wantID    string
		wantValue string
	}{
		{
			name:      "plain-result",
			body:      `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`,
			requestID: json.RawMessage(`1`),
			wantID:    `1`,
			wantValue: `"0x2a"`,
		},
		{
			name:      "result-null-is-success",
			body:      `{"jsonrpc":"2.0","id":1,"result":null}`,
			requestID: json.RawMessage(`1`),
			wantID:    `1`,
			wantValue: `null`,
		},
		{
			name:      "object-result-preserved-verbatim",
			body:      `{"jsonrpc":"2.0","id":1,"result":{"number":"0x1","extra":[true,null]}}`,
			requestID: json.RawMessage(`1`),
			wantID:    `1`,
			wantValue: `{"number":"0x1","extra":[true,null]}`,
		},
		{
			name:      "unknown-members-tolerated",
			body:      `{"jsonrpc":"2.0","id":1,"result":"0x1","x-trace":"t"}`,
			requestID: json.RawMessage(`1`),
			wantID:    `1`,
			wantValue: `"0x1"`,
		},
		{
			name:      "numeric-id-compared-numerically",
			body:      `{"jsonrpc":"2.0","id":18446744073709551616,"result":"0x1"}`,
			requestID: json.RawMessage(`18446744073709551616`),
			wantID:    `18446744073709551616`,
			wantValue: `"0x1"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := DecodeResponse([]byte(tc.body), tc.requestID)
			if err != nil {
				t.Fatalf("DecodeResponse err = %v", err)
			}
			if resp.Error != nil {
				t.Fatalf("unexpected provider error: %v", resp.Error)
			}
			if string(resp.ID) != tc.wantID {
				t.Fatalf("id = %s, want %s", resp.ID, tc.wantID)
			}
			if string(resp.Result) != tc.wantValue {
				t.Fatalf("result = %s, want %s", resp.Result, tc.wantValue)
			}
			if !json.Valid(resp.Result) {
				t.Fatalf("result is not valid JSON: %s", resp.Result)
			}
		})
	}
}

func TestDecodeResponseRejections(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		requestID json.RawMessage
		want      error
	}{
		{name: "empty-body", body: ``, requestID: json.RawMessage(`1`), want: ErrEmptyResponse},
		{name: "whitespace-body", body: "  \r\n ", requestID: json.RawMessage(`1`), want: ErrEmptyResponse},
		{
			name:      "truncated-json",
			body:      `{"jsonrpc":"2.0","id":1,`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
		{
			name:      "trailing-garbage",
			body:      `{"jsonrpc":"2.0","id":1,"result":"0x1"} extra`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
		{name: "array-body", body: `[1,2]`, requestID: json.RawMessage(`1`), want: ErrMalformedResponse},
		{name: "number-body", body: `42`, requestID: json.RawMessage(`1`), want: ErrMalformedResponse},
		{name: "null-body", body: `null`, requestID: json.RawMessage(`1`), want: ErrMalformedResponse},
		{
			name:      "missing-version",
			body:      `{"id":1,"result":"0x1"}`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
		{
			name:      "wrong-version",
			body:      `{"jsonrpc":"1.0","id":1,"result":"0x1"}`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
		{
			name:      "mismatched-id",
			body:      `{"jsonrpc":"2.0","id":999,"result":"0x1"}`,
			requestID: json.RawMessage(`1`),
			want:      ErrIDMismatch,
		},
		{
			name:      "absent-id",
			body:      `{"jsonrpc":"2.0","result":"0x1"}`,
			requestID: json.RawMessage(`1`),
			want:      ErrIDMismatch,
		},
		{
			name:      "null-id",
			body:      `{"jsonrpc":"2.0","id":null,"result":"0x1"}`,
			requestID: json.RawMessage(`1`),
			want:      ErrIDMismatch,
		},
		{
			name:      "string-id-does-not-match-number",
			body:      `{"jsonrpc":"2.0","id":"1","result":"0x1"}`,
			requestID: json.RawMessage(`1`),
			want:      ErrIDMismatch,
		},
		{
			name:      "neither-result-nor-error",
			body:      `{"jsonrpc":"2.0","id":1}`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
		{
			name:      "result-and-error-both-present",
			body:      `{"jsonrpc":"2.0","id":1,"result":"0x1","error":{"code":3,"message":"reverted"}}`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
		{
			name:      "result-and-null-error-both-present",
			body:      `{"jsonrpc":"2.0","id":1,"result":"0x1","error":null}`,
			requestID: json.RawMessage(`1`),
			want:      ErrMalformedResponse,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := DecodeResponse([]byte(tc.body), tc.requestID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if resp != nil {
				t.Fatalf("resp = %+v, want nil on rejection", resp)
			}
			// Rejections must never present as protocol errors: pool
			// counts them as transport failures, not usable answers.
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("rejection leaked a ProtocolError: %v", err)
			}
		})
	}
}

func TestDecodeResponseErrorVariants(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCode    int
		wantMessage string
		dataPresent bool
		dataRaw     string
	}{
		{
			name:        "revert-with-hex-data",
			body:        `{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted","data":"0xdead"}}`,
			wantCode:    3,
			wantMessage: "execution reverted",
			dataPresent: true,
			dataRaw:     `"0xdead"`,
		},
		{
			name:        "null-data-is-present",
			body:        `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"reverted","data":null}}`,
			wantCode:    -32000,
			wantMessage: "reverted",
			dataPresent: true,
			dataRaw:     `null`,
		},
		{
			name:        "absent-data",
			body:        `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no method"}}`,
			wantCode:    -32601,
			wantMessage: "no method",
		},
		{
			name:        "object-data-preserved",
			body:        `{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","data":{"reason":"oog"}}}`,
			wantCode:    1,
			wantMessage: "m",
			dataPresent: true,
			dataRaw:     `{"reason":"oog"}`,
		},
		{
			name:        "negative-code",
			body:        `{"jsonrpc":"2.0","id":1,"error":{"code":-32700,"message":"parse"}}`,
			wantCode:    -32700,
			wantMessage: "parse",
		},
		{
			name:        "unknown-members-tolerated",
			body:        `{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","stack":"x"}}`,
			wantCode:    1,
			wantMessage: "m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := DecodeResponse([]byte(tc.body), json.RawMessage(`1`))
			if err != nil {
				t.Fatalf("DecodeResponse err = %v", err)
			}
			if resp.Error == nil {
				t.Fatal("resp.Error = nil, want provider error")
			}
			if resp.Result != nil {
				t.Fatalf("result = %s, want nil on error response", resp.Result)
			}
			got := resp.Error
			if got.ErrorCode() != tc.wantCode {
				t.Fatalf("code = %d, want %d", got.ErrorCode(), tc.wantCode)
			}
			if got.Error() != tc.wantMessage {
				t.Fatalf("message = %q, want %q", got.Error(), tc.wantMessage)
			}
			raw, present := got.ErrorDataJSON()
			if present != tc.dataPresent {
				t.Fatalf("presence = %v, want %v", present, tc.dataPresent)
			}
			if tc.dataPresent && string(raw) != tc.dataRaw {
				t.Fatalf("data = %s, want %s", raw, tc.dataRaw)
			}
			// Presence contract: absent and null never blur. A present null
			// decodes to a nil any (same collapse as the Geth baseline),
			// which is exactly why the raw view exists.
			decoded := got.ErrorData()
			if tc.dataPresent && tc.dataRaw != "null" && decoded == nil {
				t.Fatalf("decoded data = nil, want the wire value %s", tc.dataRaw)
			}
			if !tc.dataPresent && decoded != nil {
				t.Fatalf("decoded data = %v, want nil for absent member", decoded)
			}
		})
	}
}

func TestDecodeResponseInvalidErrorMembersAreMalformed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty-error-object", body: `{"jsonrpc":"2.0","id":1,"error":{}}`},
		{name: "error-null", body: `{"jsonrpc":"2.0","id":1,"error":null}`},
		{name: "error-string", body: `{"jsonrpc":"2.0","id":1,"error":"boom"}`},
		{name: "error-array", body: `{"jsonrpc":"2.0","id":1,"error":[1]}`},
		{name: "missing-code", body: `{"jsonrpc":"2.0","id":1,"error":{"message":"m"}}`},
		{name: "string-code", body: `{"jsonrpc":"2.0","id":1,"error":{"code":"3","message":"m"}}`},
		{name: "float-code", body: `{"jsonrpc":"2.0","id":1,"error":{"code":1.5,"message":"m"}}`},
		{name: "null-code", body: `{"jsonrpc":"2.0","id":1,"error":{"code":null,"message":"m"}}`},
		{name: "missing-message", body: `{"jsonrpc":"2.0","id":1,"error":{"code":3}}`},
		{name: "number-message", body: `{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":7}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := DecodeResponse([]byte(tc.body), json.RawMessage(`1`))
			if !errors.Is(err, ErrMalformedResponse) {
				t.Fatalf("err = %v, want ErrMalformedResponse (Geth treated these as code-0 protocol errors; the native contract rejects them)", err)
			}
			if resp != nil {
				t.Fatalf("resp = %+v, want nil", resp)
			}
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("invalid error member leaked a ProtocolError: %v", err)
			}
		})
	}
}

func TestErrorSatisfiesProjectCapabilities(t *testing.T) {
	resp, err := DecodeResponse(
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted","data":"0xdead"}}`),
		json.RawMessage(`1`))
	if err != nil {
		t.Fatal(err)
	}
	wireErr := resp.Error

	var protocol rpc.ProtocolError
	if !errors.As(error(wireErr), &protocol) || protocol.ErrorCode() != 3 {
		t.Fatalf("ProtocolError capability missing or wrong code: %v", wireErr)
	}
	var dataErr rpc.DataError
	if !errors.As(error(wireErr), &dataErr) {
		t.Fatal("DataError capability missing")
	}
	if dataErr.ErrorData() != "0xdead" {
		t.Fatalf("decoded data = %v, want \"0xdead\"", dataErr.ErrorData())
	}
	var presence rpc.ErrorDataPresence
	if !errors.As(error(wireErr), &presence) {
		t.Fatal("ErrorDataPresence capability missing")
	}
	raw, present := presence.ErrorDataJSON()
	if !present || string(raw) != `"0xdead"` {
		t.Fatalf("raw data = %s/%v, want \"0xdead\"/true", raw, present)
	}
	if !strings.Contains(wireErr.Error(), "execution reverted") {
		t.Fatalf("message = %q, want the provider message", wireErr.Error())
	}
}

// FuzzDecodeResponse asserts the decoder never panics on arbitrary input and
// that its two outcomes are mutually exclusive and internally consistent.
func FuzzDecodeResponse(f *testing.F) {
	seeds := []string{
		`{"jsonrpc":"2.0","id":1,"result":"0x1"}`,
		`{"jsonrpc":"2.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted","data":"0xdead"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"m","data":null}}`,
		`{"jsonrpc":"2.0","id":1}`,
		`{"jsonrpc":"1.0","id":1,"result":"0x1"}`,
		`{"jsonrpc":"2.0","id":"1","result":"0x1"}`,
		`{"jsonrpc":"2.0","id":1,"result":"0x1","error":{"code":1,"message":"m"}}`,
		`[1,2]`, `null`, ``, `42`, `{"jsonrpc":"2.0","id":1,"error":{}}`,
	}
	for _, s := range seeds {
		f.Add(s, []byte(`1`))
	}
	f.Fuzz(func(t *testing.T, body string, requestID []byte) {
		resp, err := DecodeResponse([]byte(body), json.RawMessage(requestID))
		switch {
		case err != nil:
			if resp != nil {
				t.Fatalf("error %v returned alongside response %+v", err, resp)
			}
			classified := errors.Is(err, ErrEmptyResponse) ||
				errors.Is(err, ErrMalformedResponse) ||
				errors.Is(err, ErrIDMismatch)
			if !classified {
				t.Fatalf("unclassified decode error: %v", err)
			}
		case resp == nil:
			t.Fatal("nil response and nil error")
		case resp.Error != nil:
			if resp.Result != nil {
				t.Fatalf("error response also carries result %s", resp.Result)
			}
			if !json.Valid([]byte(resp.Error.Error())) {
				// messages are arbitrary provider strings; only assert the
				// capability accessors stay callable
				_ = resp.Error.ErrorCode()
			}
			raw, present := resp.Error.ErrorDataJSON()
			if present && !json.Valid(raw) {
				t.Fatalf("present data is not valid JSON: %s", raw)
			}
		default:
			if !json.Valid(resp.Result) {
				t.Fatalf("success response carries invalid JSON result: %s", resp.Result)
			}
			if len(resp.ID) == 0 {
				t.Fatal("success response lost its id")
			}
		}
	})
}

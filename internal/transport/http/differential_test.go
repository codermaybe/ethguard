package http

import (
	gethadapter "github.com/codermaybe/ethguard/internal/transport/geth"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// The differential suite runs the native adapter and the Geth compatibility
// adapter (geth.StandardDialer, the audited baseline) against the same
// loopback server. Class A behavior must match exactly; every intentional
// B-class divergence is asserted here and documented in
// docs/design/http-transport-differential.md §8. Geth quirks are never
// re-introduced to make a baseline pass.

// dialAdapters opens both adapters against one endpoint.
func dialAdapters(t *testing.T, rawURL string) (*Transport, transport.Transport) {
	t.Helper()
	nativeTr, err := NewDialer().DialContext(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("native dial: %v", err)
	}
	gethTr, err := gethadapter.StandardDialer{}.DialContext(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("geth dial: %v", err)
	}
	t.Cleanup(nativeTr.Close)
	t.Cleanup(gethTr.Close)
	return nativeTr.(*Transport), gethTr
}

// echoID answers with the request's id so both adapters can match responses.
func echoID(req capturedCall, payload string) []byte {
	var parsed struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal(req.Body, &parsed)
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, parsed.ID, payload))
}

// TestDifferentialEnvelopeParity locks class A: both adapters put the same
// wire envelope on the connection (method, ID, params rule, POST, headers).
func TestDifferentialEnvelopeParity(t *testing.T) {
	cases := []struct {
		name   string
		method string
		args   []any
		want   string
	}{
		{name: "no-args-omits-params", method: "eth_chainId", want: `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`},
		{name: "empty-non-nil-slice-sends-empty-array", method: "eth_chainId", args: []any{}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`},
		{name: "args-become-ordered-array", method: "eth_getBalance", args: []any{"0x1", "latest"}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x1","latest"]}`},
		{name: "transaction-by-hash-parameter", method: "eth_getTransactionByHash", args: []any{"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_getTransactionByHash","params":["0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}`},
		{name: "fee-history-ordered-parameters", method: "eth_feeHistory", args: []any{uint64(2), "0x10", []float64{50}}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_feeHistory","params":[2,"0x10",[50]]}`},
		{name: "create-access-list-pending-parameters", method: "eth_createAccessList", args: []any{map[string]any{"to": "0x0000000000000000000000000000000000000001"}, "pending"}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_createAccessList","params":[{"to":"0x0000000000000000000000000000000000000001"},"pending"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return echoID(req, `"result":"0x1"`)
			})
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			if err := call(t, nativeTr, context.Background(), nil, tc.method, tc.args...); err != nil {
				t.Fatalf("native err = %v", err)
			}
			var sink json.RawMessage
			if err := gethTr.CallContext(context.Background(), &sink, tc.method, tc.args...); err != nil {
				t.Fatalf("geth err = %v", err)
			}
			if srv.requestCount() != 2 {
				t.Fatalf("request count = %d, want one per adapter", srv.requestCount())
			}
			nativeReq, gethReq := srv.requests[0], srv.requests[1]
			for i, req := range []capturedCall{nativeReq, gethReq} {
				if req.Header.Get("Content-Type") != "application/json" || req.Header.Get("Accept") != "application/json" {
					t.Fatalf("adapter %d headers = %v", i, req.Header)
				}
			}
			if string(nativeReq.Body) != tc.want || string(gethReq.Body) != tc.want {
				t.Fatalf("wire drift:\nnative = %s\ngeth   = %s\nwant   = %s", nativeReq.Body, gethReq.Body, tc.want)
			}
		})
	}
}

// TestDifferentialTypedMethodResultParity proves that the explicit
// go-ethereum compatibility adapter and the native HTTP adapter preserve the
// same raw result payload for the representative typed methods whose request
// envelopes are covered above. The adapters differ only in response envelope
// validation quirks; a valid echoed response must project identically.
func TestDifferentialTypedMethodResultParity(t *testing.T) {
	cases := []struct {
		name   string
		method string
		args   []any
		result string
	}{
		{
			name:   "transaction-by-hash-null",
			method: "eth_getTransactionByHash",
			args:   []any{"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			result: `null`,
		},
		{
			name:   "fee-history-object",
			method: "eth_feeHistory",
			args:   []any{uint64(2), "0x10", []float64{50}},
			result: `{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.5,0.25],"reward":[["0x3"],["0x4"]]}`,
		},
		{
			name:   "create-access-list-object",
			method: "eth_createAccessList",
			args:   []any{map[string]any{"to": "0x0000000000000000000000000000000000000001"}, "pending"},
			result: `{"accessList":[],"gasUsed":"0x5208"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(_ int, req capturedCall) []byte {
				var envelope struct {
					Method string `json:"method"`
				}
				if err := json.Unmarshal(req.Body, &envelope); err != nil {
					t.Fatalf("request decode: %v", err)
				}
				if envelope.Method != tc.method {
					t.Fatalf("method = %q, want %q", envelope.Method, tc.method)
				}
				return echoID(req, `"result":`+tc.result)
			})
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			var nativeRaw, gethRaw json.RawMessage
			if err := call(t, nativeTr, context.Background(), &nativeRaw, tc.method, tc.args...); err != nil {
				t.Fatalf("native err = %v", err)
			}
			if err := gethTr.CallContext(context.Background(), &gethRaw, tc.method, tc.args...); err != nil {
				t.Fatalf("geth err = %v", err)
			}
			if string(nativeRaw) != tc.result || string(gethRaw) != tc.result {
				t.Fatalf("native/geth result = %s/%s, want %s", nativeRaw, gethRaw, tc.result)
			}
			if srv.requestCount() != 2 {
				t.Fatalf("request count = %d, want one per adapter", srv.requestCount())
			}
		})
	}
}

// TestDifferentialResponseValidationIsStrictEverywhere asserts the
// post-alignment parity: both adapters reject wrong-version and
// missing-version envelopes with the project sentinels. The id-mismatch
// cases stay baseline quirks on the compatibility channel — its single-call
// HTTP path matches responses by position and cannot see them — while the
// native adapter rejects them.
func TestDifferentialResponseValidationIsStrictEverywhere(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		gethAccepted bool
	}{
		{name: "mismatched-id", body: `{"jsonrpc":"2.0","id":999,"result":"0x2a"}`, gethAccepted: true},
		{name: "absent-id", body: `{"jsonrpc":"2.0","result":"0x2a"}`, gethAccepted: true},
		{name: "wrong-version", body: `{"jsonrpc":"1.0","id":1,"result":"0x2a"}`},
		{name: "missing-version", body: `{"id":1,"result":"0x2a"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return []byte(tc.body)
			})
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			var sink json.RawMessage
			gethErr := gethTr.CallContext(context.Background(), &sink, "eth_chainId")
			if tc.gethAccepted {
				if gethErr != nil {
					t.Fatalf("geth err = %v, want the positional baseline to accept %s", gethErr, tc.name)
				}
			} else if !errors.Is(gethErr, rpcwire.ErrMalformedResponse) {
				t.Fatalf("geth err = %v, want the malformed sentinel after alignment", gethErr)
			}
			var raw json.RawMessage
			nativeErr := call(t, nativeTr, context.Background(), &raw, "eth_chainId")
			if !errors.Is(nativeErr, rpcwire.ErrIDMismatch) && !errors.Is(nativeErr, rpcwire.ErrMalformedResponse) {
				t.Fatalf("native err = %v, want ErrIDMismatch or ErrMalformedResponse", nativeErr)
			}
			var protocol rpc.ProtocolError
			if errors.As(nativeErr, &protocol) {
				t.Fatalf("native rejection leaked a ProtocolError: %v", nativeErr)
			}
		})
	}
}

// TestDifferentialResultSemantics locks the class A result rules and the
// mutual-exclusion difference.
func TestDifferentialResultSemantics(t *testing.T) {
	t.Run("result-null-success-both", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return echoID(req, `"result":null`)
		})
		nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
		var nativeRaw, gethRaw json.RawMessage
		if err := call(t, nativeTr, context.Background(), &nativeRaw, "eth_chainId"); err != nil {
			t.Fatalf("native err = %v, want success", err)
		}
		if err := gethTr.CallContext(context.Background(), &gethRaw, "eth_chainId"); err != nil {
			t.Fatalf("geth err = %v, want success", err)
		}
		if string(nativeRaw) != "null" || string(gethRaw) != "null" {
			t.Fatalf("native/geth = %s/%s, want null/null", nativeRaw, gethRaw)
		}
	})
	t.Run("missing-result-fails-both", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return echoID(req, ``)
		})
		nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
		var nativeRaw, gethRaw json.RawMessage
		nativeErr := call(t, nativeTr, context.Background(), &nativeRaw, "eth_chainId")
		gethErr := gethTr.CallContext(context.Background(), &gethRaw, "eth_chainId")
		if nativeErr == nil || gethErr == nil {
			t.Fatalf("native/geth err = %v/%v, want both to fail", nativeErr, gethErr)
		}
		// Class A: neither may look like a usable JSON-RPC answer.
		for _, err := range []error{nativeErr, gethErr} {
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("missing result became a protocol error: %v", err)
			}
		}
		if !errors.Is(nativeErr, rpcwire.ErrMalformedResponse) {
			t.Fatalf("native err = %v, want ErrMalformedResponse", nativeErr)
		}
		// Geth's exact sentinel (gethrpc.ErrNoResult) is baseline shape, not
		// native contract: only the failure semantics must match.
	})
	t.Run("result-and-error-both-native-rejects", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return echoID(req, `"error":{"code":7,"message":"bad"},"result":"0x2a"`)
		})
		nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
		// Geth quirk (B): error wins over result.
		var gethRaw json.RawMessage
		gethErr := gethTr.CallContext(context.Background(), &gethRaw, "eth_chainId")
		var gethProtocol rpc.ProtocolError
		if !errors.As(gethErr, &gethProtocol) || gethProtocol.ErrorCode() != 7 {
			t.Fatalf("geth err = %v, want error-wins protocol error 7", gethErr)
		}
		// Native: mutual exclusion violation is malformed.
		var nativeRaw json.RawMessage
		nativeErr := call(t, nativeTr, context.Background(), &nativeRaw, "eth_chainId")
		if !errors.Is(nativeErr, rpcwire.ErrMalformedResponse) {
			t.Fatalf("native err = %v, want ErrMalformedResponse", nativeErr)
		}
	})
}

// TestDifferentialErrorDataPresence locks class A: both adapters preserve
// the absent/null/concrete three-state rule with identical raw bytes.
func TestDifferentialErrorDataPresence(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		present bool
	}{
		{name: "absent"},
		{name: "null", data: `null`, present: true},
		{name: "hex", data: `"0xdeadbeef"`, present: true},
		{name: "object", data: `{"reason":"oog"}`, present: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			member := ""
			if tc.present {
				member = `,"data":` + tc.data
			}
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return echoID(req, fmt.Sprintf(`"error":{"code":3,"message":"execution reverted"%s}`, member))
			})
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			var sink json.RawMessage
			nativeErr := call(t, nativeTr, context.Background(), &sink, "eth_chainId")
			gethErr := gethTr.CallContext(context.Background(), &sink, "eth_chainId")
			for label, err := range map[string]error{"native": nativeErr, "geth": gethErr} {
				var protocol rpc.ProtocolError
				if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 {
					t.Fatalf("%s err %v, want protocol error 3", label, err)
				}
				var presence rpc.ErrorDataPresence
				if !errors.As(err, &presence) {
					t.Fatalf("%s err %v lost ErrorDataPresence", label, err)
				}
				raw, present := presence.ErrorDataJSON()
				if present != tc.present {
					t.Fatalf("%s presence = %v, want %v", label, present, tc.present)
				}
				if tc.present && string(raw) != tc.data {
					t.Fatalf("%s raw data = %s, want %s", label, raw, tc.data)
				}
				var dataErr rpc.DataError
				if !errors.As(err, &dataErr) {
					t.Fatalf("%s err %v lost DataError", label, err)
				}
			}
		})
	}
}

// TestDifferentialDegenerateErrorsRejectedNatively asserts maintainer ruling
// 2: degenerate error members are healthy code-0 protocol errors in the Geth
// baseline (B quirk) and malformed transport failures under the native
// contract.
func TestDifferentialDegenerateErrorsRejectedNatively(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty-error-object", body: `{}`},
		{name: "error-null", body: `null`},
		{name: "error-string", body: `"boom"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return echoID(req, fmt.Sprintf(`"error":%s`, tc.body))
			})
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			// Both adapters now classify degenerate error members as
			// malformed: a code-0 protocol error would count as a healthy
			// usable answer downstream.
			var sink json.RawMessage
			gethErr := gethTr.CallContext(context.Background(), &sink, "eth_chainId")
			if !errors.Is(gethErr, rpcwire.ErrMalformedResponse) {
				t.Fatalf("geth err = %v, want the malformed sentinel after alignment", gethErr)
			}
			var gethProtocol rpc.ProtocolError
			if errors.As(gethErr, &gethProtocol) {
				t.Fatalf("geth degenerate error leaked a ProtocolError: %v", gethErr)
			}
			var nativeRaw json.RawMessage
			nativeErr := call(t, nativeTr, context.Background(), &nativeRaw, "eth_chainId")
			if !errors.Is(nativeErr, rpcwire.ErrMalformedResponse) {
				t.Fatalf("native err = %v, want ErrMalformedResponse", nativeErr)
			}
			var nativeProtocol rpc.ProtocolError
			if errors.As(nativeErr, &nativeProtocol) {
				t.Fatalf("native degenerate error leaked a ProtocolError: %v", nativeErr)
			}
		})
	}
}

// TestDifferentialHTTPStatusParity locks class A: identical status
// classification on both adapters, with the body retained in the chain.
func TestDifferentialHTTPStatusParity(t *testing.T) {
	cases := []struct {
		status    int
		rateLimit bool
	}{
		{nethttp.StatusBadRequest, false},
		{nethttp.StatusNotFound, false},
		{nethttp.StatusInternalServerError, false},
		{nethttp.StatusTooManyRequests, true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				nethttp.Error(w, `{"error":"provider says no"}`, tc.status)
			}))
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			var sink json.RawMessage
			nativeErr := call(t, nativeTr, context.Background(), &sink, "eth_chainId")
			gethErr := gethTr.CallContext(context.Background(), &sink, "eth_chainId")
			for label, err := range map[string]error{"native": nativeErr, "geth": gethErr} {
				var status rpc.HTTPStatusError
				if !errors.As(err, &status) || status.HTTPStatusCode() != tc.status {
					t.Fatalf("%s err %v, want status capability %d", label, err, tc.status)
				}
				var rateLimit rpc.RateLimitError
				hasRateLimit := errors.As(err, &rateLimit) && rateLimit.IsRateLimit()
				if hasRateLimit != tc.rateLimit {
					t.Fatalf("%s rate limit = %v, want %v", label, hasRateLimit, tc.rateLimit)
				}
				if !strings.Contains(err.Error(), "provider says no") {
					t.Fatalf("%s lost the body from the error chain: %v", label, err)
				}
				var protocol rpc.ProtocolError
				if errors.As(err, &protocol) {
					t.Fatalf("%s turned the non-2xx body into a protocol error: %v", label, err)
				}
			}
		})
	}
}

// TestDifferentialBodyLimitNativeMatchesGethClassification asserts the
// native side of the class A bounded-read rule against the shared
// ErrBodyTooLarge sentinel. The Geth side of this exact boundary is locked
// by internal/transport baseline tests (TestHTTPBaselineBodyExactLimitSucceeds,
// TestCaptureHTTPBodyTooLargeIsClassifiedErr); re-running Geth here would
// need default 32 MiB bodies, so the baseline is referenced, not repeated.
func TestDifferentialBodyLimitNativeMatchesGethClassification(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`
	t.Run("exact-limit-succeeds", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(body) })
		nativeTr, _ := dialAdapters(t, srv.srv.URL)
		nativeTr.limit = int64(len(body))
		var raw json.RawMessage
		if err := call(t, nativeTr, context.Background(), &raw, "eth_chainId"); err != nil || string(raw) != `"0x2a"` {
			t.Fatalf("val/err = %s/%v, want success at exact limit", raw, err)
		}
	})
	t.Run("one-byte-over-is-shared-sentinel", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(body) })
		nativeTr, _ := dialAdapters(t, srv.srv.URL)
		nativeTr.limit = int64(len(body) - 1)
		var raw json.RawMessage
		err := call(t, nativeTr, context.Background(), &raw, "eth_chainId")
		if !errors.Is(err, transport.ErrBodyTooLarge) {
			t.Fatalf("err = %v, want the shared ErrBodyTooLarge classification", err)
		}
		var protocol rpc.ProtocolError
		if errors.As(err, &protocol) {
			t.Fatalf("truncated prefix decoded into a protocol error: %v", err)
		}
	})
}

// TestDifferentialEmptyAndMalformedClassification asserts the shared
// semantics (fail, never usable answers) with the by-design shape difference:
// Geth surfaces raw io.EOF/encoding/json errors, the native adapter its own
// sentinels.
func TestDifferentialEmptyAndMalformedClassification(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty-body", body: ``},
		{name: "malformed-json", body: `{"jsonrpc":"2.0","id":1,`},
		{name: "array-body", body: `[1,2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return []byte(tc.body)
			})
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			var sink json.RawMessage
			nativeErr := call(t, nativeTr, context.Background(), &sink, "eth_chainId")
			gethErr := gethTr.CallContext(context.Background(), &sink, "eth_chainId")
			if nativeErr == nil || gethErr == nil {
				t.Fatalf("native/geth err = %v/%v, want both to fail", nativeErr, gethErr)
			}
			for label, err := range map[string]error{"native": nativeErr, "geth": gethErr} {
				var protocol rpc.ProtocolError
				if errors.As(err, &protocol) {
					t.Fatalf("%s classified a broken response as a protocol error: %v", label, err)
				}
			}
			if !errors.Is(nativeErr, rpcwire.ErrEmptyResponse) && !errors.Is(nativeErr, rpcwire.ErrMalformedResponse) {
				t.Fatalf("native err = %v, want project sentinel", nativeErr)
			}
			// Geth's io.EOF / *json.SyntaxError shapes stay baseline-only.
		})
	}
}

// TestDifferentialContextPrecedence locks class A on both adapters: context
// cancellation and deadlines dominate body outcomes.
func TestDifferentialContextPrecedence(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			rc := nethttp.NewResponseController(w)
			rc.Flush()
			select {
			case <-time.After(300 * time.Millisecond):
				w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
			case <-r.Context().Done():
			}
		}))
		nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		defer cancel()
		var sink json.RawMessage
		if err := call(t, nativeTr, ctx, &sink, "eth_chainId"); !errors.Is(err, context.Canceled) {
			t.Fatalf("native err = %v, want context.Canceled", err)
		}
		if err := gethTr.CallContext(ctx, &sink, "eth_chainId"); !errors.Is(err, context.Canceled) {
			t.Fatalf("geth err = %v, want context.Canceled", err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			rc := nethttp.NewResponseController(w)
			rc.Flush()
			select {
			case <-time.After(300 * time.Millisecond):
				w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
			case <-r.Context().Done():
			}
		}))
		nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var sink json.RawMessage
		if err := call(t, nativeTr, ctx, &sink, "eth_chainId"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("native err = %v, want context.DeadlineExceeded", err)
		}
		if err := gethTr.CallContext(ctx, &sink, "eth_chainId"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("geth err = %v, want context.DeadlineExceeded", err)
		}
	})
}

// TestDifferentialConcurrentCallsParity locks class A: both adapters issue
// independent POSTs with unique IDs under concurrency.
func TestDifferentialConcurrentCallsParity(t *testing.T) {
	const calls = 6
	srv := newNativeServer(t, func(n int, req capturedCall) []byte {
		return echoID(req, `"result":"0x1"`)
	})
	nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
	runConcurrent := func(label string, do func(i int) error) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make([]error, calls)
		for i := 0; i < calls; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = do(i)
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("%s call %d err = %v", label, i, err)
			}
		}
	}
	runConcurrent("native", func(i int) error {
		return call(t, nativeTr, context.Background(), nil, "eth_chainId")
	})
	runConcurrent("geth", func(i int) error {
		var sink json.RawMessage
		return gethTr.CallContext(context.Background(), &sink, "eth_chainId")
	})
	if got := srv.requestCount(); got != 2*calls {
		t.Fatalf("request count = %d, want %d (one POST per call per adapter)", got, 2*calls)
	}
	// Each adapter owns its own counter, so uniqueness holds within each
	// adapter's batch (native fires first, then geth).
	checkUnique := func(label string, batch []capturedCall) {
		t.Helper()
		seen := map[string]bool{}
		for _, req := range batch {
			var parsed struct {
				ID json.RawMessage `json:"id"`
			}
			json.Unmarshal(req.Body, &parsed)
			if seen[string(parsed.ID)] {
				t.Fatalf("%s repeats request id %s under concurrency", label, parsed.ID)
			}
			seen[string(parsed.ID)] = true
		}
	}
	checkUnique("native", srv.requests[:calls])
	checkUnique("geth", srv.requests[calls:])
}

// TestDifferentialConnectionReuseParity locks class A: both adapters ride
// one keep-alive connection across sequential calls.
func TestDifferentialConnectionReuseParity(t *testing.T) {
	srv := newNativeServer(t, func(n int, req capturedCall) []byte {
		return echoID(req, `"result":"0x1"`)
	})
	nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
	for i := 0; i < 3; i++ {
		if err := call(t, nativeTr, context.Background(), nil, "eth_chainId"); err != nil {
			t.Fatalf("native call %d err = %v", i, err)
		}
		var sink json.RawMessage
		if err := gethTr.CallContext(context.Background(), &sink, "eth_chainId"); err != nil {
			t.Fatalf("geth call %d err = %v", i, err)
		}
	}
	if got := srv.requestCount(); got != 6 {
		t.Fatalf("request count = %d, want 6", got)
	}
	// Two connections total: one per adapter. Any keep-alive failure on
	// either side would push the count above two.
	if got := srv.listener.count(); got != 2 {
		t.Fatalf("TCP connections = %d, want one keep-alive connection per adapter", got)
	}
}

// TestDifferentialStalledStatusBodyContextParity locks precedence layer 1
// across both production adapters (Slice 2 review fix): with a non-2xx body
// stalled after its headers, context cancellation and deadlines return the
// context error on the native adapter and — through the capturing transport's
// own post-read context check — on the Geth compatibility adapter as well.
// Raw Geth (without the capture layer) would surface the status error; that
// behavior is baseline-only and must not be reintroduced.
func TestDifferentialStalledStatusBodyContextParity(t *testing.T) {
	cases := []struct {
		name string
		want error
	}{
		{name: "cancel", want: context.Canceled},
		{name: "deadline", want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(nethttp.StatusInternalServerError)
				rc := nethttp.NewResponseController(w)
				rc.Flush()
				select {
				case <-time.After(300 * time.Millisecond):
					w.Write([]byte(`{"error":"late"}`))
				case <-r.Context().Done():
				}
			}))
			nativeTr, gethTr := dialAdapters(t, srv.srv.URL)
			// The cancel case fires an explicit cancel timer; the deadline
			// case relies on the timeout alone, so the two cancellation
			// causes never race for which error the context reports.
			setupCtx := func() (context.Context, context.CancelFunc) {
				if tc.want == context.Canceled {
					ctx, cancel := context.WithCancel(context.Background())
					time.AfterFunc(50*time.Millisecond, cancel)
					return ctx, cancel
				}
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			nctx, ncancel := setupCtx()
			defer ncancel()
			var nativeSink json.RawMessage
			nativeErr := call(t, nativeTr, nctx, &nativeSink, "eth_chainId")
			if !errors.Is(nativeErr, tc.want) {
				t.Fatalf("native err = %v, want %v", nativeErr, tc.want)
			}
			gctx, gcancel := setupCtx()
			defer gcancel()
			var gethSink json.RawMessage
			gethErr := gethTr.CallContext(gctx, &gethSink, "eth_chainId")
			if !errors.Is(gethErr, tc.want) {
				t.Fatalf("geth err = %v, want %v (capture-layer context precedence)", gethErr, tc.want)
			}
			for label, err := range map[string]error{"native": nativeErr, "geth": gethErr} {
				var status rpc.HTTPStatusError
				if errors.As(err, &status) {
					t.Fatalf("%s let the status classification override the context: %v", label, err)
				}
			}
		})
	}
}

// TestDifferentialCloseLifecycle asserts lifecycle parity after alignment:
// both adapters' Close is idempotent, rejects new calls with the neutral
// sentinel, and leaves in-flight calls to finish under their own context.
func TestDifferentialCloseLifecycle(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		once.Do(func() { close(started) })
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`))
	}))
	nativeTr, _ := dialAdapters(t, srv.srv.URL)

	errCh := make(chan error, 1)
	go func() {
		var raw json.RawMessage
		errCh <- call(t, nativeTr, context.Background(), &raw, "eth_chainId")
	}()
	<-started
	nativeTr.Close()
	nativeTr.Close() // idempotent

	// In-flight call finishes under its own context.
	if err := <-errCh; err != nil {
		t.Fatalf("native in-flight call failed after Close: %v", err)
	}
	// New native calls are rejected.
	var raw json.RawMessage
	if err := call(t, nativeTr, context.Background(), &raw, "eth_chainId"); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("native post-close err = %v, want ErrTransportClosed", err)
	}

	// Post-alignment parity: the compatibility adapter blocks new calls with
	// the same neutral sentinel and keeps in-flight calls running.
	gethStarted := make(chan struct{})
	gethSrv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		close(gethStarted)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`))
	}))
	_, gethTr := dialAdapters(t, gethSrv.srv.URL)
	gethErrCh := make(chan error, 1)
	go func() {
		var raw json.RawMessage
		gethErrCh <- gethTr.CallContext(context.Background(), &raw, "eth_chainId")
	}()
	<-gethStarted
	gethTr.Close()
	if err := <-gethErrCh; err != nil {
		t.Fatalf("geth in-flight call failed after Close: %v", err)
	}
	var closedRaw json.RawMessage
	if err := gethTr.CallContext(context.Background(), &closedRaw, "eth_chainId"); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("geth post-close err = %v, want the neutral connection-closed sentinel", err)
	}
}

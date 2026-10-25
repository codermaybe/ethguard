package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard/rpc"
)

// This file is the Slice 1d HTTP behavior baseline. Each test locks one
// observable behavior of the current Geth compatibility Adapter (go-ethereum
// v1.17.5, HTTP + capturing transport) so a future project-owned HTTP Adapter
// can be diffed against it. The companion matrix lives in
// docs/design/http-transport-differential.md. Nothing here changes production
// behavior; the adapter under test is the real dial path.

// capturedRequest is one request as seen by the baseline server.
type capturedRequest struct {
	Method string
	Header http.Header
	Body   []byte
}

// baselineServer is a loopback JSON-RPC server that records every request and
// answers with the nth body produced by respond (n = 0-based call order).
// Accepted TCP connections are counted at the listener so the reuse test can
// assert keep-alive behavior without touching the running server's config.
type baselineServer struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []capturedRequest
}

// countingListener counts accepted connections on the way through Accept,
// before the server goroutine ever sees them.
type countingListener struct {
	net.Listener
	mu sync.Mutex
	n  int
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.n++
		l.mu.Unlock()
	}
	return c, err
}

func (l *countingListener) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func newBaselineServer(t *testing.T, respond func(n int, req capturedRequest) []byte) *baselineServer {
	t.Helper()
	b := &baselineServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := capturedRequest{Method: r.Method, Header: r.Header.Clone(), Body: body}
		b.mu.Lock()
		n := len(b.requests)
		b.requests = append(b.requests, req)
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(respond(n, req))
	})
	b.srv = httptest.NewUnstartedServer(handler)
	listener := &countingListener{Listener: b.srv.Listener}
	b.srv.Listener = listener
	b.srv.Start()
	t.Cleanup(b.srv.Close)
	return b
}

func (b *baselineServer) requestCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.requests)
}

func (b *baselineServer) connections() int {
	return b.srv.Listener.(*countingListener).count()
}

// baselineClient dials a loopback server through the production HTTP
// compatibility path with an explicit capture limit (0 selects the default).
func baselineClient(t *testing.T, rawURL string, limit int64) Transport {
	t.Helper()
	client, ok, err := dialHTTPPreservingErrorDataWithLimit(context.Background(), rawURL, limit)
	if err != nil || !ok {
		t.Fatalf("dialHTTPPreservingErrorDataWithLimit: ok=%v err=%v, want HTTP branch with nil err", ok, err)
	}
	tr := newGethTransport(client, true)
	t.Cleanup(tr.Close)
	return tr
}

func baselineCall(t *testing.T, tr Transport, ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	t.Helper()
	var result json.RawMessage
	err := tr.CallContext(ctx, &result, method, args...)
	return result, err
}

// TestHTTPBaselineRequestEnvelope locks the exact wire form: POST with
// jsonrpc/id/method always present, params only when the caller passed args,
// and geth's fixed accept/content-type headers.
func TestHTTPBaselineRequestEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		method string
		args   []any
		want   string
	}{
		{
			name:   "no-args-omits-params",
			method: "eth_chainId",
			args:   nil,
			want:   `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
		},
		{
			name:   "args-become-array",
			method: "eth_getBalance",
			args:   []any{"0x1", "latest"},
			want:   `{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x1","latest"]}`,
		},
		{
			name:   "empty-non-nil-slice-sends-empty-array",
			method: "eth_chainId",
			args:   []any{},
			want:   `{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
				return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`)
			})
			tr := baselineClient(t, srv.srv.URL, 0)
			if _, err := baselineCall(t, tr, context.Background(), tc.method, tc.args...); err != nil {
				t.Fatalf("CallContext err = %v, want nil", err)
			}
			if srv.requestCount() != 1 {
				t.Fatalf("request count = %d, want 1", srv.requestCount())
			}
			req := srv.requests[0]
			if req.Method != http.MethodPost {
				t.Fatalf("request method = %s, want POST", req.Method)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("content-type = %q, want application/json", got)
			}
			if got := req.Header.Get("Accept"); got != "application/json" {
				t.Fatalf("accept = %q, want application/json", got)
			}
			if got := string(req.Body); got != tc.want {
				t.Fatalf("wire body = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestHTTPBaselineRequestIDIsMonotonicCounter locks geth's per-client request
// ID rule: a decimal counter starting at 1, incremented once per request on
// that client (rpc.Client.nextID). The same counter serves ChainID admission
// probes and data calls.
func TestHTTPBaselineRequestIDIsMonotonicCounter(t *testing.T) {
	srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &parsed)
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, parsed.ID))
	})
	tr := baselineClient(t, srv.srv.URL, 0)
	for want := 1; want <= 3; want++ {
		if _, err := baselineCall(t, tr, context.Background(), "eth_chainId"); err != nil {
			t.Fatalf("call %d err = %v", want, err)
		}
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(srv.requests[want-1].Body, &msg); err != nil {
			t.Fatalf("request %d not JSON: %v", want, err)
		}
		if string(msg.ID) != fmt.Sprintf("%d", want) {
			t.Fatalf("request %d id = %s, want %d", want, msg.ID, want)
		}
	}
}

// TestHTTPBaselineConcurrentCallsAreIndependentPOSTs locks the concurrency
// model: every CallContext is its own POST carrying a unique decimal ID, and
// requests are never batched at the wire level.
func TestHTTPBaselineConcurrentCallsAreIndependentPOSTs(t *testing.T) {
	const calls = 6
	srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &parsed)
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, parsed.ID))
	})
	tr := baselineClient(t, srv.srv.URL, 0)

	var wg sync.WaitGroup
	errs := make([]error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = baselineCall(t, tr, context.Background(), "eth_chainId")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d err = %v", i, err)
		}
	}
	if got := srv.requestCount(); got != calls {
		t.Fatalf("request count = %d, want %d independent POSTs", got, calls)
	}
	seen := map[string]bool{}
	for i, req := range srv.requests {
		if !json.Valid(req.Body) || strings.HasPrefix(strings.TrimSpace(string(req.Body)), "[") {
			t.Fatalf("request %d is not a single JSON object: %s", i, req.Body)
		}
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &msg)
		if seen[string(msg.ID)] {
			t.Fatalf("duplicate request id %s across concurrent calls", msg.ID)
		}
		seen[string(msg.ID)] = true
	}
}

// TestHTTPBaselineNormalResponseForms locks how success shapes decode:
// result:null is a success whose raw value is the bytes "null", and a
// response without a result member is geth's ErrNoResult sentinel.
func TestHTTPBaselineNormalResponseForms(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantVal   string
		wantNoRes bool
	}{
		{name: "result-null-is-success", body: `{"jsonrpc":"2.0","id":1,"result":null}`, wantVal: "null"},
		{name: "missing-result-is-ErrNoResult", body: `{"jsonrpc":"2.0","id":1}`, wantNoRes: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
				return []byte(tc.body)
			})
			tr := baselineClient(t, srv.srv.URL, 0)
			val, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if tc.wantNoRes {
				if !errors.Is(err, gethrpc.ErrNoResult) {
					t.Fatalf("err = %v, want gethrpc.ErrNoResult", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil for %s result", err, tc.wantVal)
			}
			if string(val) != tc.wantVal {
				t.Fatalf("result = %s, want %s", val, tc.wantVal)
			}
		})
	}
}

// TestHTTPBaselineEmptyBodyAndStatus204AreEOF locks the 2xx decode path: an
// empty body (explicit 200 or a 204, which passes the 2xx status gate) fails
// in json.Decoder as io.EOF, not as HTTPError or ErrNoResult.
func TestHTTPBaselineEmptyBodyAndStatus204AreEOF(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "200-empty-body", status: 200},
		{name: "204-no-content", status: 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)
			tr := baselineClient(t, srv.URL, 0)
			_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if !errors.Is(err, io.EOF) {
				t.Fatalf("err = %v, want io.EOF from decoding an empty 2xx body", err)
			}
			var httpErr gethrpc.HTTPError
			if errors.As(err, &httpErr) {
				t.Fatalf("empty 2xx body became HTTPError: %v", err)
			}
		})
	}
}

// TestHTTPBaselineMalformedBodySurfacesJSONError locks that a 2xx body geth
// cannot decode surfaces as the raw encoding/json error, not as a JSON-RPC
// protocol error and not as a preserved error-data wrapper. Both malformed
// forms are locked: garbage inside a value is *json.SyntaxError, while input
// truncated at EOF is io.ErrUnexpectedEOF — neither is classified further.
func TestHTTPBaselineMalformedBodySurfacesJSONError(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		match func(error) bool
	}{
		{name: "garbage-in-value", body: `{"jsonrpc":"2.0","id":1,}}`, match: func(err error) bool {
			var syntax *json.SyntaxError
			return errors.As(err, &syntax)
		}},
		{name: "array-not-object", body: `[1,2]`, match: func(err error) bool {
			var typeErr *json.UnmarshalTypeError
			return errors.As(err, &typeErr)
		}},
		{name: "truncated-at-eof", body: `{"jsonrpc":"2.0","id":1,`, match: func(err error) bool {
			return errors.Is(err, io.ErrUnexpectedEOF)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
				return []byte(tc.body)
			})
			tr := baselineClient(t, srv.srv.URL, 0)
			_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if err == nil {
				t.Fatal("err = nil, want a decoding error")
			}
			if !tc.match(err) {
				t.Fatalf("err = %v (%T), want the raw encoding/json decode error", err, err)
			}
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("malformed body classified as JSON-RPC protocol error: %v", err)
			}
			var preserved *preservedDataError
			if errors.As(err, &preserved) {
				t.Fatalf("malformed body wrapped as preserved error data: %v", err)
			}
		})
	}
}

// TestHTTPBaselineResponseIDAndVersionNotValidated locks a geth HTTP quirk:
// the single-call HTTP path matches responses by position (one response body
// per call) and never checks the response's id or jsonrpc version against the
// request. A future adapter may tighten this only with a maintainer ruling.
func TestHTTPBaselineResponseIDAndVersionNotValidated(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "mismatched-id", body: `{"jsonrpc":"2.0","id":999,"result":"0x2a"}`},
		{name: "null-id", body: `{"jsonrpc":"2.0","id":null,"result":"0x2a"}`},
		{name: "wrong-version", body: `{"jsonrpc":"1.0","id":1,"result":"0x2a"}`},
		{name: "no-version", body: `{"id":1,"result":"0x2a"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
				return []byte(tc.body)
			})
			tr := baselineClient(t, srv.srv.URL, 0)
			val, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if err != nil {
				t.Fatalf("err = %v, want success despite %s", err, tc.name)
			}
			if string(val) != `"0x2a"` {
				t.Fatalf("result = %s, want \"0x2a\"", val)
			}
		})
	}
}

// TestHTTPBaselineErrorDataVariants locks the error-decode surface of the
// compatibility adapter: raw presence via ErrorDataJSON, decoded value via
// geth's DataError, the code capability, and the absence of any HTTP-status
// wrapper on the 2xx path.
func TestHTTPBaselineErrorDataVariants(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		present bool
	}{
		{name: "object", data: `{"reason":"out of gas"}`, present: true},
		{name: "array", data: `[1,2]`, present: true},
		{name: "number", data: `42`, present: true},
		{name: "string", data: `"oops"`, present: true},
		{name: "null", data: `null`, present: true},
		{name: "absent", data: "", present: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			member := ""
			if tc.present {
				member = `,"data":` + tc.data
			}
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted"%s}}`, member)
			srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
				return []byte(body)
			})
			tr := baselineClient(t, srv.srv.URL, 0)
			_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if err == nil {
				t.Fatal("err = nil, want the JSON-RPC error")
			}
			var protocol rpc.ProtocolError
			if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 {
				t.Fatalf("err %v lost the code capability or code, want ProtocolError code 3", err)
			}
			var preserved *preservedDataError
			if !errors.As(err, &preserved) {
				t.Fatalf("err = %v (%T), want preservedDataError", err, err)
			}
			raw, present := preserved.ErrorDataJSON()
			if present != tc.present {
				t.Fatalf("presence = %v, want %v", present, tc.present)
			}
			if tc.present && string(raw) != tc.data {
				t.Fatalf("raw data = %s, want %s", raw, tc.data)
			}
			// geth's own decoded error stays reachable through the chain, so
			// ethclient.RevertErrorData and rpc.DataError keep working. A
			// present null decodes to a nil any: that collapse is exactly why
			// the raw-preserving wrapper exists.
			var dataErr rpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatalf("err %v lost the geth DataError view", err)
			}
			if tc.present && tc.data != "null" && dataErr.ErrorData() == nil {
				t.Fatalf("decoded data = nil, want the wire value %s", tc.data)
			}
			var gethData gethrpc.DataError
			if !errors.As(err, &gethData) {
				t.Fatalf("err %v lost gethrpc.DataError", err)
			}
			var status interface{ HTTPStatusCode() int }
			if errors.As(err, &status) {
				t.Fatalf("2xx JSON-RPC error gained an HTTP status capability: %v", err)
			}
		})
	}
}

// TestHTTPBaselineErrorMemberDegenerateForms locks how the adapter classifies
// error members that geth's jsonError decode cannot populate: code 0 and the
// "json-rpc error 0" message. These still satisfy ProtocolError, so pool
// counts them as usable answers and (code 0 not being a server-error code)
// records them as healthy observations.
func TestHTTPBaselineErrorMemberDegenerateForms(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty-error-object", body: `{"jsonrpc":"2.0","id":1,"error":{}}`},
		{name: "error-null", body: `{"jsonrpc":"2.0","id":1,"error":null}`},
		{name: "error-non-object", body: `{"jsonrpc":"2.0","id":1,"error":"boom"}`},
		{name: "error-and-result-both-present", body: `{"jsonrpc":"2.0","id":1,"error":{"code":7,"message":"bad"},"result":"0x2a"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
				return []byte(tc.body)
			})
			tr := baselineClient(t, srv.srv.URL, 0)
			_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if err == nil {
				t.Fatalf("err = nil, want a JSON-RPC error for %s", tc.name)
			}
			var protocol rpc.ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("err = %v (%T), want ProtocolError", err, err)
			}
			wantCode := 0
			if tc.name == "error-and-result-both-present" {
				wantCode = 7 // error wins over result in geth's decode order
			}
			if protocol.ErrorCode() != wantCode {
				t.Fatalf("code = %d, want %d", protocol.ErrorCode(), wantCode)
			}
			if wantCode == 0 && err.Error() != "json-rpc error 0" {
				t.Fatalf("message = %q, want geth's \"json-rpc error 0\"", err.Error())
			}
		})
	}
}

// TestHTTPBaselineNon2xxBecomesHTTPErrorWithBody locks the HTTP status layer:
// every non-2xx status becomes gethrpc.HTTPError (status text + body in the
// chain, wrapped with the project's status/rate-limit capabilities). The body
// is carried verbatim but never parsed as a JSON-RPC error: even a body with
// error.data yields no preservedDataError.
func TestHTTPBaselineNon2xxBecomesHTTPErrorWithBody(t *testing.T) {
	cases := []struct {
		status     int
		statusText string
	}{
		{http.StatusBadRequest, "400 Bad Request"},
		{http.StatusUnauthorized, "401 Unauthorized"},
		{http.StatusForbidden, "403 Forbidden"},
		{http.StatusNotFound, "404 Not Found"},
		{http.StatusInternalServerError, "500 Internal Server Error"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			bodyText := `{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted","data":"0xdead"}}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, bodyText, tc.status)
			}))
			t.Cleanup(srv.Close)
			tr := baselineClient(t, srv.URL, 0)
			_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
			if err == nil {
				t.Fatalf("err = nil, want HTTPError for status %d", tc.status)
			}
			var httpErr gethrpc.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("err = %v (%T), want gethrpc.HTTPError in chain", err, err)
			}
			if httpErr.StatusCode != tc.status || httpErr.Status != tc.statusText {
				t.Fatalf("status = %d/%s, want %d/%s", httpErr.StatusCode, httpErr.Status, tc.status, tc.statusText)
			}
			if string(httpErr.Body) != bodyText+"\n" {
				t.Fatalf("HTTPError body = %q, want the response body verbatim", httpErr.Body)
			}
			var status interface{ HTTPStatusCode() int }
			if !errors.As(err, &status) || status.HTTPStatusCode() != tc.status {
				t.Fatalf("err %v lost the HTTPStatusCode capability", err)
			}
			var rateLimit interface{ IsRateLimit() bool }
			if errors.As(err, &rateLimit) && rateLimit.IsRateLimit() {
				t.Fatalf("status %d must not be classified as rate limit", tc.status)
			}
			// The JSON-RPC error body must NOT be parsed into a protocol error
			// on the non-2xx path: pool sees a transport failure, not a usable
			// answer, and no error-data presence is claimed.
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("non-2xx body became a JSON-RPC protocol error: %v", err)
			}
			var preserved *preservedDataError
			if errors.As(err, &preserved) {
				t.Fatalf("non-2xx body yielded preserved error data %s", preservedBodyText(t, preserved))
			}
		})
	}
}

// TestHTTPBaseline429CarriesRateLimitCapability locks that only 429 sets the
// rate-limit bit on the status wrapper; pool's monitor treats it as explicit
// throttling (backoff doubling) rather than plain unreachability.
func TestHTTPBaseline429CarriesRateLimitCapability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"slow down"}`, http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	tr := baselineClient(t, srv.URL, 0)
	_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
	var status interface {
		error
		HTTPStatusCode() int
		IsRateLimit() bool
	}
	if !errors.As(err, &status) || !status.IsRateLimit() || status.HTTPStatusCode() != 429 {
		t.Fatalf("err = %v (%T), want rate-limit capability with status 429", err, err)
	}
	// The 429 body still rides the HTTPError verbatim.
	var httpErr gethrpc.HTTPError
	if !errors.As(err, &httpErr) || !strings.Contains(httpErr.Error(), "slow down") {
		t.Fatalf("429 body missing from error chain: %v", err)
	}
}

// TestHTTPBaselineBodyExactLimitSucceeds locks the capture boundary: a body
// of exactly limit bytes is served to geth and succeeds, while the same body
// against a one-byte-smaller limit is refused as ErrBodyTooLarge (the
// truncated prefix never reaches the decoder).
func TestHTTPBaselineBodyExactLimitSucceeds(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`
	t.Run("exact-limit-is-success", func(t *testing.T) {
		srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
			return []byte(body)
		})
		tr := baselineClient(t, srv.srv.URL, int64(len(body)))
		val, err := baselineCall(t, tr, context.Background(), "eth_chainId")
		if err != nil || string(val) != `"0x2a"` {
			t.Fatalf("val/err = %s/%v, want success at the exact limit", val, err)
		}
	})
	t.Run("one-byte-over-is-ErrBodyTooLarge", func(t *testing.T) {
		srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
			return []byte(body)
		})
		tr := baselineClient(t, srv.srv.URL, int64(len(body)-1))
		_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
		if !errors.Is(err, ErrBodyTooLarge) {
			t.Fatalf("err = %v, want ErrBodyTooLarge one byte over the limit", err)
		}
	})
}

// TestHTTPBaselineDeadlineExceededDominates locks precedence layer 1 for
// deadlines: a deadline that fires while an oversized body stalls is returned
// as context.DeadlineExceeded, never as a body or decode classification.
func TestHTTPBaselineDeadlineExceededDominates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush headers so the capture read blocks on the body itself.
		w.Header().Set("Content-Type", "application/json")
		rc := http.NewResponseController(w)
		rc.Flush()
		select {
		case <-time.After(300 * time.Millisecond):
			w.Write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted","data":"0x%s"}}`,
				strings.Repeat("a", 8192))))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	tr := baselineClient(t, srv.URL, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := baselineCall(t, tr, ctx, "eth_chainId")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("deadline was overridden by the truncation outcome: %v", err)
	}
	var preserved *preservedDataError
	if errors.As(err, &preserved) {
		t.Fatalf("deadline produced preserved error data %s", preservedBodyText(t, preserved))
	}
}

// TestHTTPBaselineHTTPCloseIsNoOp locks the lifecycle difference between HTTP
// and WSS: rpc.Client.Close returns immediately for HTTP, aborts nothing, and
// leaves the adapter usable. A call that is in flight when Close runs still
// completes successfully, and later calls keep working.
func TestHTTPBaselineHTTPCloseIsNoOp(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`))
	}))
	t.Cleanup(srv.Close)
	tr := baselineClient(t, srv.URL, 0)

	errCh := make(chan error, 1)
	go func() {
		_, err := baselineCall(t, tr, context.Background(), "eth_chainId")
		errCh <- err
	}()
	<-started
	tr.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("in-flight call aborted by Close: %v", err)
	}
	val, err := baselineCall(t, tr, context.Background(), "eth_chainId")
	if err != nil || string(val) != `"0x2a"` {
		t.Fatalf("post-close call = %s/%v, want success (HTTP close is a no-op)", val, err)
	}
}

// TestHTTPBaselineConnectionReuseAcrossCalls locks the transport-layer
// lifecycle: the adapter dials once at construction, and sequential calls
// ride HTTP keep-alive instead of opening a fresh TCP connection each time.
func TestHTTPBaselineConnectionReuseAcrossCalls(t *testing.T) {
	srv := newBaselineServer(t, func(n int, req capturedRequest) []byte {
		return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)
	})
	tr := baselineClient(t, srv.srv.URL, 0)
	for i := 0; i < 3; i++ {
		if _, err := baselineCall(t, tr, context.Background(), "eth_chainId"); err != nil {
			t.Fatalf("call %d err = %v", i, err)
		}
	}
	if got := srv.requestCount(); got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}
	if got := srv.connections(); got != 1 {
		t.Fatalf("TCP connections = %d, want 1 keep-alive connection across calls", got)
	}
}

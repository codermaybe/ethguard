package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// TestDialerSchemeValidation locks the dial surface: http/https dial without
// network I/O, anything else is rejected with a project-owned error.
func TestDialerSchemeValidation(t *testing.T) {
	cases := []struct {
		rawURL  string
		wantErr bool
	}{
		{rawURL: "http://127.0.0.1:1"},
		{rawURL: "https://example.invalid"},
		{rawURL: "ws://127.0.0.1:1", wantErr: true},
		{rawURL: "wss://example.invalid", wantErr: true},
		{rawURL: "/tmp/socket", wantErr: true},
		{rawURL: "://bad", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.rawURL, func(t *testing.T) {
			tr, err := NewDialer().DialContext(context.Background(), tc.rawURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("dial = %v, want error", tr)
				}
				return
			}
			if err != nil {
				t.Fatalf("dial err = %v", err)
			}
			tr.Close()
		})
	}
}

// nativeServer records requests and answers with the nth body produced by
// respond. Connections are counted at the listener so reuse can be asserted.
type nativeServer struct {
	srv      *httptest.Server
	listener *countingListener

	mu       sync.Mutex
	requests []capturedCall
}

type capturedCall struct {
	Header nethttp.Header
	Body   []byte
}

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

// newNativeServerHandler starts the recording server with an explicit
// handler, configured before Start so no field is written on a live server.
func newNativeServerHandler(t *testing.T, handler nethttp.Handler) *nativeServer {
	t.Helper()
	s := &nativeServer{}
	s.srv = httptest.NewUnstartedServer(handler)
	s.listener = &countingListener{Listener: s.srv.Listener}
	s.srv.Listener = s.listener
	s.srv.Start()
	t.Cleanup(s.srv.Close)
	return s
}

func newNativeServer(t *testing.T, respond func(n int, req capturedCall) []byte) *nativeServer {
	t.Helper()
	var s *nativeServer
	s = newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, _ := io.ReadAll(r.Body)
		req := capturedCall{Header: r.Header.Clone(), Body: body}
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(respond(n, req))
	}))
	return s
}

func (s *nativeServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// dialNative dials the server through the native adapter with an explicit
// body limit (0 selects the default).
func dialNative(t *testing.T, rawURL string, limit int64) *Transport {
	t.Helper()
	tr, err := Dialer{BodyLimit: limit}.DialContext(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(tr.Close)
	return tr.(*Transport)
}

func call(t *testing.T, tr *Transport, ctx context.Context, result any, method string, args ...any) error {
	t.Helper()
	return tr.CallContext(ctx, result, method, args...)
}

// TestNativeRequestEnvelope locks the native wire envelope: identical to the
// audited Geth class A behavior (POST, fixed content headers, params omitted
// when nil, [] for an empty non-nil slice, ordered array for args).
func TestNativeRequestEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		method string
		args   []any
		want   string
	}{
		{name: "no-args-omits-params", method: "eth_chainId", want: `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`},
		{name: "empty-non-nil-slice-sends-empty-array", method: "eth_chainId", args: []any{}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`},
		{name: "args-become-ordered-array", method: "eth_getBalance", args: []any{"0x1", "latest"}, want: `{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x1","latest"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`)
			})
			tr := dialNative(t, srv.srv.URL, 0)
			if err := call(t, tr, context.Background(), nil, tc.method, tc.args...); err != nil {
				t.Fatalf("CallContext err = %v", err)
			}
			if srv.requestCount() != 1 {
				t.Fatalf("request count = %d, want 1", srv.requestCount())
			}
			req := srv.requests[0]
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("content-type = %q, want application/json", got)
			}
			if got := req.Header.Get("Accept"); got != "application/json" {
				t.Fatalf("accept = %q, want application/json", got)
			}
			if string(req.Body) != tc.want {
				t.Fatalf("wire = %s, want %s", req.Body, tc.want)
			}
		})
	}
}

// TestNativeResultForms locks result decoding: raw passthrough for
// *json.RawMessage, typed decode, null target ignored, and result:null as a
// success carrying the bytes "null".
func TestNativeResultForms(t *testing.T) {
	t.Run("raw-result", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)
		})
		tr := dialNative(t, srv.srv.URL, 0)
		var raw json.RawMessage
		if err := call(t, tr, context.Background(), &raw, "eth_chainId"); err != nil {
			t.Fatal(err)
		}
		if string(raw) != `"0x2a"` {
			t.Fatalf("raw = %s", raw)
		}
	})
	t.Run("typed-result", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)
		})
		tr := dialNative(t, srv.srv.URL, 0)
		var hex string
		if err := call(t, tr, context.Background(), &hex, "eth_chainId"); err != nil {
			t.Fatal(err)
		}
		if hex != "0x2a" {
			t.Fatalf("hex = %q", hex)
		}
	})
	t.Run("nil-target-ignores-result", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return []byte(`{"jsonrpc":"2.0","id":1,"result":{"a":1}}`)
		})
		tr := dialNative(t, srv.srv.URL, 0)
		if err := call(t, tr, context.Background(), nil, "eth_chainId"); err != nil {
			t.Fatalf("nil target err = %v", err)
		}
	})
	t.Run("result-null-is-success-raw-null", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return []byte(`{"jsonrpc":"2.0","id":1,"result":null}`)
		})
		tr := dialNative(t, srv.srv.URL, 0)
		var raw json.RawMessage
		if err := call(t, tr, context.Background(), &raw, "eth_chainId"); err != nil {
			t.Fatalf("result null err = %v, want success", err)
		}
		if string(raw) != "null" {
			t.Fatalf("raw = %s, want null", raw)
		}
	})
	t.Run("non-pointer-target-rejected", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`)
		})
		tr := dialNative(t, srv.srv.URL, 0)
		var s string
		if err := call(t, tr, context.Background(), s, "eth_chainId"); err == nil {
			t.Fatal("non-pointer target accepted")
		}
	})
}

// TestNativeResponseValidation locks the strict contract that Geth lacks:
// wrong or missing response id, wrong version, mutual-exclusion violations,
// and missing results are rejected with the rpcwire sentinels and never
// surface as protocol errors.
func TestNativeResponseValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{name: "mismatched-id", body: `{"jsonrpc":"2.0","id":999,"result":"0x1"}`, want: rpcwire.ErrIDMismatch},
		{name: "absent-id", body: `{"jsonrpc":"2.0","result":"0x1"}`, want: rpcwire.ErrIDMismatch},
		{name: "wrong-version", body: `{"jsonrpc":"1.0","id":1,"result":"0x1"}`, want: rpcwire.ErrMalformedResponse},
		{name: "missing-version", body: `{"id":1,"result":"0x1"}`, want: rpcwire.ErrMalformedResponse},
		{name: "result-and-error", body: `{"jsonrpc":"2.0","id":1,"result":"0x1","error":{"code":3,"message":"m"}}`, want: rpcwire.ErrMalformedResponse},
		{name: "neither-result-nor-error", body: `{"jsonrpc":"2.0","id":1}`, want: rpcwire.ErrMalformedResponse},
		{
			name: "degenerate-error-object",
			// Geth treated this as a healthy code-0 protocol error; the
			// native contract rejects it as malformed (intentional B-class
			// difference, maintainer ruling 2).
			body: `{"jsonrpc":"2.0","id":1,"error":{}}`,
			want: rpcwire.ErrMalformedResponse,
		},
		{
			name: "error-null",
			body: `{"jsonrpc":"2.0","id":1,"error":null}`,
			want: rpcwire.ErrMalformedResponse,
		},
		{
			name: "error-missing-message",
			body: `{"jsonrpc":"2.0","id":1,"error":{"code":3}}`,
			want: rpcwire.ErrMalformedResponse,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte {
				return []byte(tc.body)
			})
			tr := dialNative(t, srv.srv.URL, 0)
			var raw json.RawMessage
			err := call(t, tr, context.Background(), &raw, "eth_chainId")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("rejection leaked a ProtocolError: %v", err)
			}
		})
	}
}

// TestNativeErrorDataPresence locks the three-state rule on the live path:
// absent, JSON null, and concrete data keep their wire distinction, and the
// returned error satisfies ProtocolError, DataError, and ErrorDataPresence.
func TestNativeErrorDataPresence(t *testing.T) {
	cases := []struct {
		name        string
		data        string
		present     bool
		wantDecoded any
	}{
		{name: "absent"},
		{name: "null", data: `null`, present: true, wantDecoded: nil},
		{name: "hex", data: `"0xdeadbeef"`, present: true, wantDecoded: "0xdeadbeef"},
		{name: "object", data: `{"reason":"oog"}`, present: true, wantDecoded: map[string]any{"reason": "oog"}},
		{name: "number", data: `42`, present: true, wantDecoded: float64(42)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			member := ""
			if tc.present {
				member = `,"data":` + tc.data
			}
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted"%s}}`, member)
			srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(body) })
			tr := dialNative(t, srv.srv.URL, 0)
			var raw json.RawMessage
			err := call(t, tr, context.Background(), &raw, "eth_chainId")
			var protocol rpc.ProtocolError
			if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 {
				t.Fatalf("err %v, want ProtocolError code 3", err)
			}
			var presence rpc.ErrorDataPresence
			if !errors.As(err, &presence) {
				t.Fatalf("err %v lost ErrorDataPresence", err)
			}
			got, present := presence.ErrorDataJSON()
			if present != tc.present {
				t.Fatalf("presence = %v, want %v", present, tc.present)
			}
			if tc.present && string(got) != tc.data {
				t.Fatalf("raw data = %s, want %s", got, tc.data)
			}
			var dataErr rpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatalf("err %v lost DataError", err)
			}
			if tc.wantDecoded == nil {
				if dataErr.ErrorData() != nil {
					t.Fatalf("decoded = %v, want nil", dataErr.ErrorData())
				}
			} else if fmt.Sprint(dataErr.ErrorData()) != fmt.Sprint(tc.wantDecoded) {
				t.Fatalf("decoded = %v, want %v", dataErr.ErrorData(), tc.wantDecoded)
			}
			var status rpc.HTTPStatusError
			if errors.As(err, &status) {
				t.Fatalf("2xx provider error gained an HTTP status capability: %v", err)
			}
		})
	}
}

// TestNativeHTTPStatus locks the status layer: non-2xx becomes a project
// status error with bounded body in the chain, never a protocol error; only
// 429 is rate-limited.
func TestNativeHTTPStatus(t *testing.T) {
	cases := []struct {
		status     int
		statusText string
		rateLimit  bool
	}{
		{nethttp.StatusBadRequest, "400 Bad Request", false},
		{nethttp.StatusUnauthorized, "401 Unauthorized", false},
		{nethttp.StatusForbidden, "403 Forbidden", false},
		{nethttp.StatusNotFound, "404 Not Found", false},
		{nethttp.StatusInternalServerError, "500 Internal Server Error", false},
		{nethttp.StatusTooManyRequests, "429 Too Many Requests", true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d", tc.status), func(t *testing.T) {
			bodyText := `{"error":"provider says no"}`
			srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				nethttp.Error(w, bodyText, tc.status)
			}))
			tr := dialNative(t, srv.srv.URL, 0)
			var raw json.RawMessage
			err := call(t, tr, context.Background(), &raw, "eth_chainId")
			var status rpc.HTTPStatusError
			if !errors.As(err, &status) || status.HTTPStatusCode() != tc.status {
				t.Fatalf("err = %v (%T), want status capability with code %d", err, err, tc.status)
			}
			var rateLimit rpc.RateLimitError
			hasRateLimit := errors.As(err, &rateLimit) && rateLimit.IsRateLimit()
			if hasRateLimit != tc.rateLimit {
				t.Fatalf("rate limit = %v, want %v", hasRateLimit, tc.rateLimit)
			}
			// Body rides the message for text-based classification rules.
			if !strings.Contains(err.Error(), "provider says no") {
				t.Fatalf("body missing from error chain: %v", err)
			}
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("non-2xx body became a protocol error: %v", err)
			}
			var presence rpc.ErrorDataPresence
			if errors.As(err, &presence) {
				t.Fatalf("non-2xx body claimed error-data presence: %v", err)
			}
		})
	}
}

// TestNativeBodyLimit locks the bounded-read contract: a body of exactly
// limit bytes succeeds, one byte over is ErrBodyTooLarge, and a truncated
// prefix never decodes into a provider error.
func TestNativeBodyLimit(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`
	t.Run("exact-limit-succeeds", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(body) })
		tr := dialNative(t, srv.srv.URL, int64(len(body)))
		var raw json.RawMessage
		if err := call(t, tr, context.Background(), &raw, "eth_chainId"); err != nil || string(raw) != `"0x2a"` {
			t.Fatalf("val/err = %s/%v, want success at exact limit", raw, err)
		}
	})
	t.Run("one-byte-over-is-body-too-large", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(body) })
		tr := dialNative(t, srv.srv.URL, int64(len(body)-1))
		var raw json.RawMessage
		err := call(t, tr, context.Background(), &raw, "eth_chainId")
		if !errors.Is(err, transport.ErrBodyTooLarge) {
			t.Fatalf("err = %v, want ErrBodyTooLarge", err)
		}
	})
	t.Run("oversized-error-body-never-decodes", func(t *testing.T) {
		// An oversized provider error must surface as ErrBodyTooLarge, not
		// as the revert it would decode into if the truncated prefix were
		// parsed.
		srv := newNativeServer(t, func(n int, req capturedCall) []byte {
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted","data":"0x%s"}}`,
				strings.Repeat("a", 8192)))
		})
		tr := dialNative(t, srv.srv.URL, 64)
		var raw json.RawMessage
		err := call(t, tr, context.Background(), &raw, "eth_chainId")
		if !errors.Is(err, transport.ErrBodyTooLarge) {
			t.Fatalf("err = %v, want ErrBodyTooLarge", err)
		}
		var protocol rpc.ProtocolError
		if errors.As(err, &protocol) {
			t.Fatalf("truncated body decoded into a protocol error: %v", err)
		}
	})
}

// TestNativeEmptyAndMalformedBodies locks the replacement of Geth's raw
// io.EOF and encoding/json errors with project-owned classifications.
func TestNativeEmptyAndMalformedBodies(t *testing.T) {
	t.Run("empty-2xx-body", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte { return nil })
		tr := dialNative(t, srv.srv.URL, 0)
		var raw json.RawMessage
		err := call(t, tr, context.Background(), &raw, "eth_chainId")
		if !errors.Is(err, rpcwire.ErrEmptyResponse) {
			t.Fatalf("err = %v, want ErrEmptyResponse", err)
		}
		if errors.Is(err, io.EOF) {
			t.Fatalf("empty body surfaced as Geth's raw io.EOF: %v", err)
		}
	})
	t.Run("malformed-json", func(t *testing.T) {
		srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(`{"jsonrpc":"2.0","id":1,`) })
		tr := dialNative(t, srv.srv.URL, 0)
		var raw json.RawMessage
		err := call(t, tr, context.Background(), &raw, "eth_chainId")
		if !errors.Is(err, rpcwire.ErrMalformedResponse) {
			t.Fatalf("err = %v, want ErrMalformedResponse", err)
		}
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			t.Fatalf("malformed body surfaced as a raw encoding/json error: %v", err)
		}
	})
}

// TestNativeContextPrecedence locks precedence layer 1: cancellation and
// deadline dominate body outcomes.
func TestNativeContextPrecedence(t *testing.T) {
	t.Run("cancel-during-stalled-body", func(t *testing.T) {
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
		tr := dialNative(t, srv.srv.URL, 64)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		defer cancel()
		var raw json.RawMessage
		if err := call(t, tr, ctx, &raw, "eth_chainId"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("deadline-beats-oversized-body", func(t *testing.T) {
		srv := newNativeServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			rc := nethttp.NewResponseController(w)
			rc.Flush()
			select {
			case <-time.After(300 * time.Millisecond):
				w.Write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"late","data":"0x%s"}}`,
					strings.Repeat("a", 8192))))
			case <-r.Context().Done():
			}
		}))
		tr := dialNative(t, srv.srv.URL, 64)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var raw json.RawMessage
		err := call(t, tr, ctx, &raw, "eth_chainId")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		if errors.Is(err, transport.ErrBodyTooLarge) {
			t.Fatalf("deadline was overridden by the truncation outcome: %v", err)
		}
	})
}

// TestNativeConcurrentCalls locks concurrency safety: unique IDs across
// concurrent calls, independent POSTs, all succeed.
func TestNativeConcurrentCalls(t *testing.T) {
	const calls = 8
	srv := newNativeServer(t, func(n int, req capturedCall) []byte {
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &parsed)
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, parsed.ID))
	})
	tr := dialNative(t, srv.srv.URL, 0)
	var wg sync.WaitGroup
	errs := make([]error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var raw json.RawMessage
			errs[i] = call(t, tr, context.Background(), &raw, "eth_chainId")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d err = %v", i, err)
		}
	}
	if got := srv.requestCount(); got != calls {
		t.Fatalf("request count = %d, want %d", got, calls)
	}
	seen := map[string]bool{}
	for i, req := range srv.requests {
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &parsed)
		if seen[string(parsed.ID)] {
			t.Fatalf("request %d repeats id %s", i, parsed.ID)
		}
		seen[string(parsed.ID)] = true
	}
}

// TestNativeConnectionReuse locks keep-alive behavior: sequential calls ride
// one TCP connection.
func TestNativeConnectionReuse(t *testing.T) {
	srv := newNativeServer(t, func(n int, req capturedCall) []byte {
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &parsed)
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, parsed.ID))
	})
	tr := dialNative(t, srv.srv.URL, 0)
	for i := 0; i < 3; i++ {
		if err := call(t, tr, context.Background(), nil, "eth_chainId"); err != nil {
			t.Fatalf("call %d err = %v", i, err)
		}
	}
	if got := srv.requestCount(); got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}
	if got := srv.listener.count(); got != 1 {
		t.Fatalf("TCP connections = %d, want 1 keep-alive connection", got)
	}
}

// TestNativeClose locks the native lifecycle, deliberately different from
// the Geth no-op: Close is idempotent, releases idle connections, rejects
// new calls, and leaves in-flight calls to finish under their own context.
func TestNativeClose(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		once.Do(func() { close(started) })
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`))
	}))
	t.Cleanup(srv.Close)
	tr := dialNative(t, srv.URL, 0)

	errCh := make(chan error, 1)
	go func() {
		var raw json.RawMessage
		errCh <- tr.CallContext(context.Background(), &raw, "eth_chainId")
	}()
	<-started
	// Idempotent: a double close must not panic.
	tr.Close()
	tr.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("in-flight call failed after Close: %v", err)
	}
	var raw json.RawMessage
	if err := tr.CallContext(context.Background(), &raw, "eth_chainId"); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("post-close err = %v, want ErrTransportClosed", err)
	}
}

// TestNativeChainID locks the identity probe: a plain eth_chainId call with
// the project-owned quantity decoder.
func TestNativeChainID(t *testing.T) {
	srv := newNativeServer(t, func(n int, req capturedCall) []byte {
		return []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`)
	})
	tr := dialNative(t, srv.srv.URL, 0)
	chainID, err := tr.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID err = %v", err)
	}
	if chainID.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("chain id = %s, want 1", chainID)
	}
	// The identity probe rides the same envelope rules: no params member.
	if got := string(srv.requests[0].Body); got != `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}` {
		t.Fatalf("chain id wire = %s", got)
	}
}

func TestDecodeHexQuantity(t *testing.T) {
	cases := []struct {
		raw     string
		want    *big.Int
		wantErr bool
	}{
		{raw: `"0x0"`, want: big.NewInt(0)},
		{raw: `"0x1"`, want: big.NewInt(1)},
		{raw: `"0x1f"`, want: big.NewInt(31)},
		{raw: `"0xFF"`, want: big.NewInt(255)},
		{raw: `"0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"`, want: func() *big.Int {
			v, _ := new(big.Int).SetString("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", 16)
			return v
		}()},
		{raw: `"1"`, wantErr: true},
		{raw: `"0x"`, wantErr: true},
		{raw: `"0X1"`, wantErr: true},
		{raw: `"0x01"`, wantErr: true},
		{raw: `"0x1g"`, wantErr: true},
		{raw: `1`, wantErr: true},
		{raw: `null`, wantErr: true},
		{raw: `"0x1.5"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := decodeHexQuantity(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("decode = %s, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode err = %v", err)
			}
			if got.Cmp(tc.want) != 0 {
				t.Fatalf("decode = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestNativeChainIDQuantityRejections locks that broken identity answers are
// failures, not zero-valued chain ids.
func TestNativeChainIDQuantityRejections(t *testing.T) {
	cases := []string{
		`{"jsonrpc":"2.0","id":1,"result":"0x01"}`,
		`{"jsonrpc":"2.0","id":1,"result":"1"}`,
		`{"jsonrpc":"2.0","id":1,"result":1}`,
		`{"jsonrpc":"2.0","id":1,"result":null}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			srv := newNativeServer(t, func(n int, req capturedCall) []byte { return []byte(body) })
			tr := dialNative(t, srv.srv.URL, 0)
			if _, err := tr.ChainID(context.Background()); err == nil {
				t.Fatal("ChainID accepted a non-canonical quantity")
			}
		})
	}
}

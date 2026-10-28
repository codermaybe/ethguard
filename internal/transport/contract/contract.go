// Package contract verifies the L1 transport behavior every connection
// adapter must share. It is a test harness, not production code: adapters run
// the suite from their own tests against an in-process scripted server.
//
// The suite is normative for the EthGuard-native contract
// (docs/design/transport-contract.md): malformed bodies fail with the
// project-owned sentinels, context outcomes dominate body outcomes, Close is
// idempotent and blocks new calls, and one call is exactly one JSON-RPC
// request with no retry. Adapter-specific diagnostics (error text, body
// retention sizes) are L3 and stay outside this suite.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// Adapter describes one transport adapter under test.
type Adapter struct {
	// Name labels the suite's subtests.
	Name string
	// Dial returns one live transport against the harness endpoint URL.
	Dial func(t *testing.T, rawURL string) transport.Transport
	// DialWithBodyLimit returns a transport whose response-body bound is
	// limit bytes, so the oversized-body case stays cheap. A nil hook makes
	// the suite run that case against the adapter's default bound with a
	// bound+1 body.
	DialWithBodyLimit func(t *testing.T, rawURL string, limit int64) transport.Transport
}

// RunL1 executes every L1 behavioral clause against the adapter. Each clause
// gets its own scripted server, so cases cannot observe each other.
func RunL1(t *testing.T, adapter Adapter) {
	t.Helper()
	t.Run(adapter.Name, func(t *testing.T) {
		t.Run("call delivers one json-rpc request and the result", func(t *testing.T) {
			testCallDeliversResult(t, adapter)
		})
		t.Run("result target contract", func(t *testing.T) {
			testResultTargetContract(t, adapter)
		})
		t.Run("protocol error capabilities", func(t *testing.T) {
			testProtocolErrorCapabilities(t, adapter)
		})
		t.Run("protocol error data presence", func(t *testing.T) {
			testProtocolErrorDataPresence(t, adapter)
		})
		t.Run("degenerate error members are malformed responses", func(t *testing.T) {
			testDegenerateErrorMembers(t, adapter)
		})
		t.Run("http status classification", func(t *testing.T) {
			testHTTPStatusClassification(t, adapter)
		})
		t.Run("empty and malformed bodies", func(t *testing.T) {
			testEmptyAndMalformedBodies(t, adapter)
		})
		t.Run("trailing data rejected", func(t *testing.T) {
			testTrailingData(t, adapter)
		})
		t.Run("duplicate keys last win", func(t *testing.T) {
			testDuplicateKeys(t, adapter)
		})
		t.Run("context precedence", func(t *testing.T) {
			testContextPrecedence(t, adapter)
		})
		t.Run("body limit", func(t *testing.T) {
			testBodyLimit(t, adapter)
		})
		t.Run("close lifecycle", func(t *testing.T) {
			testCloseLifecycle(t, adapter)
		})
		t.Run("in-flight call survives close", func(t *testing.T) {
			testInFlightSurvivesClose(t, adapter)
		})
		t.Run("concurrent calls", func(t *testing.T) {
			testConcurrentCalls(t, adapter)
		})
		t.Run("chain id probe", func(t *testing.T) {
			testChainID(t, adapter)
		})
		t.Run("no automatic retry", func(t *testing.T) {
			testNoAutoRetry(t, adapter)
		})
	})
}

// server is one scripted JSON-RPC endpoint. It records every request body,
// then dispatches to the scenario handler, which sees the one-based request
// number and the raw body and owns the whole response. Recording happens
// before the handler runs, so count and request stay available even for
// status-only responses, and a handler installed after construction still
// goes through the recorder.
type server struct {
	*httptest.Server

	mu     sync.Mutex
	bodies [][]byte
	handle func(w http.ResponseWriter, r *http.Request, n int, body []byte)
}

// respondWith builds a handler answering every request with the same raw
// bytes under a JSON content type.
func respondWith(body []byte) func(*http.Request) (int, []byte) {
	return func(*http.Request) (int, []byte) { return 0, body }
}

func newServer(t *testing.T, respond func(n int, body []byte) (int, []byte)) *server {
	t.Helper()
	return newRawServer(t, func(w http.ResponseWriter, r *http.Request, n int, reqBody []byte) {
		status, out := respond(n, reqBody)
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	})
}

// newRawServer installs the recording wrapper around an unfiltered handler.
func newRawServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int, body []byte)) *server {
	t.Helper()
	s := &server{handle: handle}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		s.mu.Lock()
		n := len(s.bodies) + 1
		s.bodies = append(s.bodies, body)
		handle := s.handle
		s.mu.Unlock()
		handle(w, r, n, body)
	}))
	t.Cleanup(s.Close)
	return s
}

// setStatus replaces the response behavior with a bare HTTP status. The
// exchange still counts as a recorded request.
func (s *server) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handle = func(w http.ResponseWriter, r *http.Request, n int, body []byte) {
		w.WriteHeader(status)
	}
}

// hang makes the server accept the request, flush the head, and stall until
// the request context ends or the wait elapses, then write the given bytes.
func (s *server) hang(writeAfter time.Duration, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handle = func(w http.ResponseWriter, r *http.Request, n int, reqBody []byte) {
		rc := http.NewResponseController(w)
		rc.Flush()
		select {
		case <-time.After(writeAfter):
			w.Write(body)
		case <-r.Context().Done():
		}
	}
}

// count reports how many requests the server received so far.
func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

// request returns the raw body of the one-based nth request.
func (s *server) request(n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[n-1]
}

// envelope builds a success response echoing the request's id, so concurrent
// calls with distinct ids each receive their own.
func envelope(req []byte, result string) (int, []byte) {
	return 0, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, requestID(req), result))
}

func errorEnvelope(req []byte, code int, message string) (int, []byte) {
	return 0, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q}}`, requestID(req), code, message))
}

// requestID extracts the id member from a recorded request body; a missing
// id echoes null so the adapter's id check fails loudly instead of the
// harness hiding it.
func requestID(req []byte) string {
	var wire struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(req, &wire); err != nil || len(wire.ID) == 0 {
		return "null"
	}
	return string(wire.ID)
}

// requestEnvelope decodes the harness side of the wire: method, params, and
// the request id, so callers can assert the request shape without tying the
// suite to an encoder.
type requestEnvelope struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     json.RawMessage   `json:"id"`
}

func testCallDeliversResult(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) {
		return envelope(body, `"0x1"`)
	})
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	var raw json.RawMessage
	if err := tr.CallContext(context.Background(), &raw, "eth_chainId"); err != nil {
		t.Fatalf("CallContext: %v", err)
	}
	if string(raw) != `"0x1"` {
		t.Fatalf("result = %s, want the raw result bytes", raw)
	}
	if srv.count() != 1 {
		t.Fatalf("server saw %d requests, want exactly one", srv.count())
	}
	var req requestEnvelope
	requested := srv.request(1)
	if err := json.Unmarshal(requested, &req); err != nil {
		t.Fatalf("request body %s is not a JSON object: %v", requested, err)
	}
	if req.Method != "eth_chainId" {
		t.Fatalf("wire method = %q, want eth_chainId", req.Method)
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		t.Fatalf("request id missing: %s", srv.bodies[0])
	}
}

func testResultTargetContract(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) { return envelope(body, `{"a":1}`) })
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	// nil target: the call succeeds and the result is discarded.
	if err := tr.CallContext(context.Background(), nil, "eth_chainId"); err != nil {
		t.Fatalf("nil result target: %v", err)
	}
	// A non-pointer target is a caller error, not a transport outcome.
	if err := tr.CallContext(context.Background(), map[string]any{}, "eth_chainId"); err == nil {
		t.Fatal("non-pointer result target must fail")
	}
}

func testProtocolErrorCapabilities(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) {
		return errorEnvelope(body, -32000, "execution reverted")
	})
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	var raw json.RawMessage
	err := tr.CallContext(context.Background(), &raw, "eth_call")
	var protocol rpc.ProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("err = %v, want a ProtocolError capability", err)
	}
	if protocol.ErrorCode() != -32000 {
		t.Fatalf("code = %d, want -32000", protocol.ErrorCode())
	}
	if !strings.Contains(err.Error(), "execution reverted") {
		t.Fatalf("error text %q lost the provider message", err)
	}
	var dataErr rpc.DataError
	if !errors.As(err, &dataErr) {
		t.Fatalf("err = %v, lost the DataError capability", err)
	}
	var presence rpc.ErrorDataPresence
	if errors.As(err, &presence) {
		if _, present := presence.ErrorDataJSON(); present {
			t.Fatal("error without a data member must report absence")
		}
	}
}

func testProtocolErrorDataPresence(t *testing.T, adapter Adapter) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		envelop []byte
		present bool
		raw     string
	}{
		{name: "string-data", envelop: []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"revert","data":"0x08c379a0"}}`), present: true, raw: `"0x08c379a0"`},
		{name: "null-data", envelop: []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"revert","data":null}}`), present: true, raw: "null"},
		{name: "object-data", envelop: []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"revert","data":{"inner":1}}}`), present: true, raw: `{"inner":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(n int, body []byte) (int, []byte) { return 0, tc.envelop })
			tr := adapter.Dial(t, srv.URL)
			defer tr.Close()
			var raw json.RawMessage
			err := tr.CallContext(context.Background(), &raw, "eth_call")
			var presence rpc.ErrorDataPresence
			if !errors.As(err, &presence) {
				t.Fatalf("err = %v, want the ErrorDataPresence capability", err)
			}
			got, present := presence.ErrorDataJSON()
			if present != tc.present {
				t.Fatalf("presence = %v, want %v", present, tc.present)
			}
			if present && string(got) != tc.raw {
				t.Fatalf("raw data = %s, want %s", got, tc.raw)
			}
			var dataErr rpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatalf("err = %v, lost the DataError capability", err)
			}
		})
	}
}

// Degenerate error members are malformed transport responses, never healthy
// protocol errors: a code-0 error would count as a usable answer downstream.
func testDegenerateErrorMembers(t *testing.T, adapter Adapter) {
	t.Helper()
	for _, tc := range []struct {
		name  string
		error string
	}{
		{name: "empty-error-object", error: `{}`},
		{name: "error-null", error: `null`},
		{name: "error-string", error: `"boom"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(n int, body []byte) (int, []byte) {
				return 0, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":%s}`, requestID(body), tc.error))
			})
			tr := adapter.Dial(t, srv.URL)
			defer tr.Close()
			var raw json.RawMessage
			err := tr.CallContext(context.Background(), &raw, "eth_chainId")
			if err == nil {
				t.Fatal("degenerate error member must fail the call")
			}
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("degenerate error member surfaced as a protocol error: %v", err)
			}
			if !errors.Is(err, rpcwire.ErrMalformedResponse) {
				t.Fatalf("err = %v, want the malformed-response sentinel", err)
			}
		})
	}
}

func testHTTPStatusClassification(t *testing.T, adapter Adapter) {
	t.Helper()
	for _, tc := range []struct {
		name      string
		status    int
		rateLimit bool
	}{
		{name: "server-error", status: 500},
		{name: "not-found", status: 404},
		{name: "rate-limit", status: 429, rateLimit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(n int, body []byte) (int, []byte) { return tc.status, nil })
			tr := adapter.Dial(t, srv.URL)
			defer tr.Close()
			var raw json.RawMessage
			err := tr.CallContext(context.Background(), &raw, "eth_chainId")
			var statusErr rpc.HTTPStatusError
			if !errors.As(err, &statusErr) {
				t.Fatalf("err = %v, want the HTTPStatusError capability", err)
			}
			if statusErr.HTTPStatusCode() != tc.status {
				t.Fatalf("status = %d, want %d", statusErr.HTTPStatusCode(), tc.status)
			}
			var rate rpc.RateLimitError
			if tc.rateLimit && !errors.As(err, &rate) {
				t.Fatalf("429 must carry the RateLimitError capability: %v", err)
			}
			if !tc.rateLimit {
				if rateLimited, isRate := err.(rpc.RateLimitError); isRate && rateLimited.IsRateLimit() {
					t.Fatalf("non-429 status must not claim rate limiting: %v", err)
				}
			}
		})
	}
}

func testEmptyAndMalformedBodies(t *testing.T, adapter Adapter) {
	t.Helper()
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{name: "empty-body", body: ``, want: rpcwire.ErrEmptyResponse},
		{name: "malformed-json", body: `{"jsonrpc":"2.0","id":1,`, want: rpcwire.ErrMalformedResponse},
		{name: "array-body", body: `[1,2]`, want: rpcwire.ErrMalformedResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(n int, body []byte) (int, []byte) { return 0, []byte(tc.body) })
			tr := adapter.Dial(t, srv.URL)
			defer tr.Close()
			var raw json.RawMessage
			err := tr.CallContext(context.Background(), &raw, "eth_chainId")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var protocol rpc.ProtocolError
			if errors.As(err, &protocol) {
				t.Fatalf("a broken response must not surface as a protocol error: %v", err)
			}
		})
	}
}

// Trailing garbage after a complete JSON-RPC response is malformed, never a
// usable result: a truncated or spliced body must not vouch for anything.
func testTrailingData(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) {
		return 0, []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"} trailing`)
	})
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	var raw json.RawMessage
	err := tr.CallContext(context.Background(), &raw, "eth_chainId")
	if !errors.Is(err, rpcwire.ErrMalformedResponse) {
		t.Fatalf("err = %v, want the malformed-response sentinel", err)
	}
}

// Duplicate object keys resolve last-wins on the wire decoder. The category
// is shared behavior; the diagnostic description of the duplicate is L3.
func testDuplicateKeys(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) {
		return 0, []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1","result":"0x2"}`)
	})
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	var raw json.RawMessage
	err := tr.CallContext(context.Background(), &raw, "eth_chainId")
	if err != nil {
		t.Fatalf("duplicate keys: %v", err)
	}
	if string(raw) != `"0x2"` {
		t.Fatalf("duplicate-key resolution = %s, want the last spelling", raw)
	}
}

func testContextPrecedence(t *testing.T, adapter Adapter) {
	t.Helper()
	t.Run("canceled-before-call", func(t *testing.T) {
		srv := newServer(t, func(n int, body []byte) (int, []byte) { return envelope(body, `"0x1"`) })
		tr := adapter.Dial(t, srv.URL)
		defer tr.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var raw json.RawMessage
		if err := tr.CallContext(ctx, &raw, "eth_chainId"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("deadline-beats-hung-server", func(t *testing.T) {
		srv := newServer(t, func(n int, body []byte) (int, []byte) { return 0, nil })
		srv.hang(2*time.Second, nil)
		tr := adapter.Dial(t, srv.URL)
		defer tr.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		var raw json.RawMessage
		if err := tr.CallContext(ctx, &raw, "eth_chainId"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
	})
	t.Run("cancel-beats-status-outcome", func(t *testing.T) {
		srv := newRawServer(t, statusBodyHandler(500, 2*time.Second, []byte("late error body")))
		tr := adapter.Dial(t, srv.URL)
		defer tr.Close()
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		defer cancel()
		var raw json.RawMessage
		if err := tr.CallContext(ctx, &raw, "eth_chainId"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want the context error to dominate the status outcome", err)
		}
	})
}

func testBodyLimit(t *testing.T, adapter Adapter) {
	t.Helper()
	if adapter.DialWithBodyLimit != nil {
		srv := newServer(t, func(n int, body []byte) (int, []byte) {
			return 0, []byte(`"0x` + strings.Repeat("a", 256) + `"`)
		})
		tr := adapter.DialWithBodyLimit(t, srv.URL, 64)
		defer tr.Close()
		var raw json.RawMessage
		err := tr.CallContext(context.Background(), &raw, "eth_chainId")
		if !errors.Is(err, transport.ErrBodyTooLarge) {
			t.Fatalf("err = %v, want the neutral body-limit sentinel", err)
		}
		return
	}
	// Adapters with a fixed audited bound: one request over the default bound.
	srv := newServer(t, func(n int, body []byte) (int, []byte) {
		return 0, []byte(`"0x` + strings.Repeat("a", int(defaultLimitBytes)) + `"`)
	})
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	var raw json.RawMessage
	err := tr.CallContext(context.Background(), &raw, "eth_chainId")
	if !errors.Is(err, transport.ErrBodyTooLarge) {
		t.Fatalf("err = %v, want the neutral body-limit sentinel", err)
	}
}

// Close is idempotent, blocks new calls with the neutral sentinel, and never
// touches calls already in flight.
func testCloseLifecycle(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) { return envelope(body, `"0x1"`) })
	tr := adapter.Dial(t, srv.URL)
	tr.Close()
	tr.Close() // idempotent: no panic, no change in state
	var raw json.RawMessage
	err := tr.CallContext(context.Background(), &raw, "eth_chainId")
	if !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("post-close err = %v, want the neutral connection-closed sentinel", err)
	}
	if srv.count() != 0 {
		t.Fatalf("closed transport contacted the server %d times", srv.count())
	}
}

func testInFlightSurvivesClose(t *testing.T, adapter Adapter) {
	t.Helper()
	started := make(chan struct{})
	srv := newRawServer(t, func(w http.ResponseWriter, r *http.Request, n int, body []byte) {
		close(started)
		rc := http.NewResponseController(w)
		rc.Flush()
		select {
		case <-time.After(300 * time.Millisecond):
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case <-r.Context().Done():
		}
	})
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	done := make(chan error, 1)
	go func() {
		var raw json.RawMessage
		done <- tr.CallContext(context.Background(), &raw, "eth_chainId")
	}()
	<-started
	tr.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("in-flight call failed after Close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight call did not complete")
	}
}

func testConcurrentCalls(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) { return envelope(body, `"0x1"`) })
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	const parallel = 8
	var wg sync.WaitGroup
	errs := make(chan error, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var raw json.RawMessage
			if err := tr.CallContext(context.Background(), &raw, "eth_chainId"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}
}

// ChainID probes the chain identity through exactly one eth_chainId call and
// the shared error capabilities.
func testChainID(t *testing.T, adapter Adapter) {
	t.Helper()
	t.Run("succeeds", func(t *testing.T) {
		srv := newServer(t, func(n int, body []byte) (int, []byte) { return envelope(body, `"0x1"`) })
		tr := adapter.Dial(t, srv.URL)
		defer tr.Close()
		chainID, err := tr.ChainID(context.Background())
		if err != nil {
			t.Fatalf("ChainID: %v", err)
		}
		if chainID.Cmp(big.NewInt(1)) != 0 {
			t.Fatalf("chain id = %s, want 1", chainID)
		}
		var req requestEnvelope
		if err := json.Unmarshal(srv.request(1), &req); err != nil || req.Method != "eth_chainId" {
			t.Fatalf("chain probe method = %q (err %v), want eth_chainId", req.Method, err)
		}
	})
	t.Run("error capabilities", func(t *testing.T) {
		srv := newServer(t, func(n int, body []byte) (int, []byte) { return 500, nil })
		tr := adapter.Dial(t, srv.URL)
		defer tr.Close()
		if _, err := tr.ChainID(context.Background()); err == nil {
			t.Fatal("chain probe must fail on a server error")
		} else {
			var statusErr rpc.HTTPStatusError
			if !errors.As(err, &statusErr) {
				t.Fatalf("chain probe err = %v, want the HTTPStatusError capability", err)
			}
		}
	})
}

// One call is one JSON-RPC request: a failed exchange is never retried or
// replayed by the transport.
func testNoAutoRetry(t *testing.T, adapter Adapter) {
	t.Helper()
	srv := newServer(t, func(n int, body []byte) (int, []byte) { return 500, nil })
	tr := adapter.Dial(t, srv.URL)
	defer tr.Close()
	var raw json.RawMessage
	if err := tr.CallContext(context.Background(), &raw, "eth_chainId"); err == nil {
		t.Fatal("expected the failing exchange to fail")
	}
	if got := srv.count(); got != 1 {
		t.Fatalf("server saw %d requests for one call, want exactly one (no retry)", got)
	}
}

// defaultLimitBytes is the number of body bytes the no-hook oversized-body
// case writes: one byte over the audited default bound.
const defaultLimitBytes = 32<<20 + 1

// statusBodyHandler writes a bare status, flushes it, and then stalls before
// writing the late body, so a caller can cancel mid-read.
func statusBodyHandler(status int, writeAfter time.Duration, body []byte) func(http.ResponseWriter, *http.Request, int, []byte) {
	return func(w http.ResponseWriter, r *http.Request, n int, reqBody []byte) {
		w.WriteHeader(status)
		rc := http.NewResponseController(w)
		rc.Flush()
		select {
		case <-time.After(writeAfter):
			w.Write(body)
		case <-r.Context().Done():
		}
	}
}

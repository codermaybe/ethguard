package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

type rawBodyTestRPCError struct{ code int }

func (e rawBodyTestRPCError) Error() string { return "execution reverted" }

func (e rawBodyTestRPCError) ErrorCode() int { return e.code }

func TestPreserveErrorDataDistinguishesMissingAndNull(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		present bool
		data    string
	}{
		{name: "missing", body: `{"error":{"code":3,"message":"execution reverted"}}`},
		{name: "null", body: `{"error":{"code":3,"message":"execution reverted","data":null}}`, present: true, data: "null"},
		{name: "hex", body: `{"error":{"code":3,"message":"execution reverted","data":"0xdead"}}`, present: true, data: `"0xdead"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := preserveErrorData(rawBodyTestRPCError{code: 3}, []byte(tc.body))
			var preserved *preservedDataError
			if !errors.As(err, &preserved) {
				t.Fatalf("error type = %T, want preservedDataError", err)
			}
			got, gotPresent := preserved.ErrorDataJSON()
			if gotPresent != tc.present || string(got) != tc.data {
				t.Fatalf("data = %s/%v, want %s/%v", got, gotPresent, tc.data, tc.present)
			}
			if tc.present && !json.Valid(got) {
				t.Fatalf("preserved data is not JSON: %s", got)
			}
		})
	}
}

// newCaptureJSONRPCServer runs an in-process JSON-RPC server that echoes the
// request id back (geth matches responses by id) and answers every request
// with the bytes of writeBody. Loopback only; no external network I/O.
func newCaptureJSONRPCServer(t *testing.T, writeBody func(id json.RawMessage) []byte) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(writeBody(req.ID))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newCaptureErrorServer is newCaptureJSONRPCServer with a JSON-RPC error
// body whose data member is exactly data when present (data must be a
// pre-encoded JSON value such as "null" or "\"0xdead\""), or absent
// otherwise. size pads the data with hex characters to force a body beyond a
// small capture limit.
func newCaptureErrorServer(t *testing.T, data string, present bool, size int) *httptest.Server {
	payload := data
	if size > 0 {
		payload = `"0x` + strings.Repeat("a", size) + `"`
	}
	member := ""
	if present {
		member = `,"data":` + payload
	}
	return newCaptureJSONRPCServer(t, func(id json.RawMessage) []byte {
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":3,"message":"execution reverted"%s}}`, id, member))
	})
}

// dialCaptureServer dials the server through the capturing transport with an
// explicit capture limit (0 selects the default) and expects the HTTP branch
// to be taken.
func dialCaptureServer(t *testing.T, rawURL string, limit int64) *ethclient.Client {
	client, ok, err := dialHTTPPreservingErrorDataWithLimit(context.Background(), rawURL, limit)
	if err != nil || !ok {
		t.Fatalf("dialHTTPPreservingErrorDataWithLimit: ok=%v err=%v, want ok and nil err", ok, err)
	}
	t.Cleanup(client.Close)
	return client
}

func callCapture(t *testing.T, client *ethclient.Client, ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	err := client.Client().CallContext(ctx, &result, "eth_chainId")
	return result, err
}

// callWithCapture mirrors collectOne's calling convention: every call gets a
// fresh single-slot box bound to its own context, so the transport actually
// captures (a box-less call is a transparent pass-through).
func callWithCapture(t *testing.T, client *ethclient.Client, base context.Context) (json.RawMessage, error) {
	t.Helper()
	src := &Source{client: client}
	return collectOne(base, src, "eth_chainId", nil)
}

// preservedBodyText details the data of a preserved error for failure
// messages: "absent" or the raw bytes.
func preservedBodyText(t *testing.T, preserved *preservedDataError) string {
	t.Helper()
	data, ok := preserved.ErrorDataJSON()
	if !ok {
		return "absent"
	}
	return string(data)
}

// TestCaptureHTTPNormalStates drives the capturing transport over in-process
// HTTP and asserts the normal outcome set: a clean success result, plus
// error bodies carrying missing data, JSON null data, empty "0x", and
// concrete hex data. Each lands on preservedDataError with exact presence.
func TestCaptureHTTPNormalStates(t *testing.T) {
	cases := []struct {
		name    string
		result  bool // true answers with a result, otherwise an error body
		data    string
		present bool
	}{
		{name: "success-result", result: true},
		{name: "missing-data"},
		{name: "null-data", data: "null", present: true},
		{name: "empty-hex-data", data: `"0x"`, present: true},
		{name: "hex-data", data: `"0xdeadbeef"`, present: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var srv *httptest.Server
			if tc.result {
				srv = newCaptureJSONRPCServer(t, func(id json.RawMessage) []byte {
					return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x2a"}`, id))
				})
			} else {
				srv = newCaptureErrorServer(t, tc.data, tc.present, 0)
			}
			client := dialCaptureServer(t, srv.URL, 0)
			result, err := callWithCapture(t, client, context.Background())
			if tc.result {
				if err != nil {
					t.Fatalf("CallContext err = %v, want nil", err)
				}
				if string(result) != `"0x2a"` {
					t.Fatalf("result = %s, want \"0x2a\"", result)
				}
				return
			}
			var preserved *preservedDataError
			if !errors.As(err, &preserved) {
				t.Fatalf("error type = %T (%v), want preservedDataError", err, err)
			}
			got, gotPresent := preserved.ErrorDataJSON()
			want := ""
			if tc.present {
				want = tc.data
			}
			if gotPresent != tc.present || string(got) != want {
				t.Fatalf("data = %s/%v, want %s/%v", got, gotPresent, want, tc.present)
			}
		})
	}
}

// TestCaptureHTTPBodyTooLargeIsClassifiedErr locks the truncation contract:
// a body beyond the capture limit must surface as ErrBodyTooLarge, and the
// truncated prefix must never flow into preserveErrorData, so no JSON-RPC
// error (revert code 3) may be derived from it.
func TestCaptureHTTPBodyTooLargeIsClassifiedErr(t *testing.T) {
	srv := newCaptureErrorServer(t, "", true, 8192)
	client := dialCaptureServer(t, srv.URL, 512)
	_, err := callWithCapture(t, client, context.Background())
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	// The truncated prefix must never reach preserveErrorData: because the
	// oversized read refuses to install a body, the box stays empty and
	// collectOne's body.bytes() lookup finds nothing to vouch for. That the
	// returned error still classifies as ErrBodyTooLarge — and never parses
	// back into a revert (code 3) error — is asserted above and below.
	var preserved *preservedDataError
	if errors.As(err, &preserved) {
		t.Fatalf("truncated body was parsed as a preserved JSON-RPC error with data %s", preservedBodyText(t, preserved))
	}
	if ctxErr := context.Background().Err(); ctxErr != nil {
		t.Fatalf("background context unexpectedly done: %v", ctxErr)
	}
}

// TestCaptureHTTPReadFailureSurfacesReadError checks precedence layer 3: a
// peer that closes mid-body without an overrun surfaces its read error, and
// it is not mistaken for a classification of its own.
func TestCaptureHTTPReadFailureSurfacesReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		_ = req
		conn, bufrw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			http.Error(w, "hijack failed", http.StatusInternalServerError)
			return
		}
		defer conn.Close()
		// Headers promise 1000 bytes; supply a fragment, then hang up.
		bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{\"jsonrpc\":")
		bufrw.Flush()
		conn.Close()
	}))
	t.Cleanup(srv.Close)
	client := dialCaptureServer(t, srv.URL, 0)
	_, err := callWithCapture(t, client, context.Background())
	if err == nil {
		t.Fatal("CallContext err = nil, want the mid-body read failure")
	}
	if errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("read failure misclassified as ErrBodyTooLarge: %v", err)
	}
	var preserved *preservedDataError
	if errors.As(err, &preserved) {
		t.Fatalf("read failure turned into a preserved error with data %s", preservedBodyText(t, preserved))
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read failure turned into a context error: %v", err)
	}
}

// TestCaptureHTTPContextCancelBeatsTruncation checks precedence layer 1: the
// response header arrives immediately, the oversized body is delayed too
// long vs the cancel timer, and the caller must see the context error rather
// than ErrBodyTooLarge, even though the pending body was beyond the limit.
func TestCaptureHTTPContextCancelBeatsTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		_ = req
		// Flush headers so the transport hands the response over and the
		// capture read blocks on the body itself.
		w.Header().Set("Content-Type", "application/json")
		rc := http.NewResponseController(w)
		rc.Flush()
		select {
		case <-time.After(200 * time.Millisecond):
			w.Write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":3,"message":"execution reverted","data":"0x%s"}}`,
				req.ID, strings.Repeat("a", 8192))))
		case <-r.Context().Done():
			return
		}
	}))
	t.Cleanup(srv.Close)
	client := dialCaptureServer(t, srv.URL, 64)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	defer cancel()
	_, err := callWithCapture(t, client, ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to dominate the truncation outcome", err)
	}
	if errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("cancellation was overridden by the truncation error: %v", err)
	}
}

// TestCaptureHTTPRecoveryAfterOversizedBody is the error-body recovery state:
// an oversized capture fails one call, and the very next call on the same
// client observes a normal, complete result.
func TestCaptureHTTPRecoveryAfterOversizedBody(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := newCaptureJSONRPCServer(t, func(id json.RawMessage) []byte {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":3,"message":"execution reverted","data":"0x%s"}}`,
				id, strings.Repeat("a", 8192)))
		}
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x2a"}`, id))
	})
	client := dialCaptureServer(t, srv.URL, 64)
	if _, err := callWithCapture(t, client, context.Background()); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("first call err = %v, want ErrBodyTooLarge", err)
	}
	result, err := callWithCapture(t, client, context.Background())
	if err != nil {
		t.Fatalf("second call err = %v, want nil", err)
	}
	if string(result) != `"0x2a"` {
		t.Fatalf("second call result = %s, want \"0x2a\"", result)
	}
}

// TestRawBodyBoxSingleSlotRejectsSecondSet enforces the single-slot
// invariant directly: the first set wins, a second set is refused, and the
// box still serves the first capture.
func TestRawBodyBoxSingleSlotRejectsSecondSet(t *testing.T) {
	box := &rawBodyBox{}
	if err := box.set([]byte("first")); err != nil {
		t.Fatalf("first set err = %v, want nil", err)
	}
	if err := box.set([]byte("second")); !errors.Is(err, errRawBodySlotTaken) {
		t.Fatalf("second set err = %v, want errRawBodySlotTaken", err)
	}
	body, ok := box.bytes()
	if !ok || string(body) != "first" {
		t.Fatalf("box = %s/%v, want first capture kept", body, ok)
	}
}

// TestCaptureTransportSecondCaptureFailsRoundTrip drives the invariant
// through the transport: a second RoundTrip on a context whose box already
// holds a capture must fail with errRawBodySlotTaken instead of silently
// re-attributing a response to the call.
func TestCaptureTransportSecondCaptureFailsRoundTrip(t *testing.T) {
	srv := newCaptureJSONRPCServer(t, func(id json.RawMessage) []byte {
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x2a"}`, id))
	})
	ctx, box := withRawBody(context.Background())
	tr := capturingTransport{}
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(`{"id":1}`))
		if err != nil {
			t.Fatalf("NewRequestWithContext: %v", err)
		}
		resp, err := tr.RoundTrip(req)
		if i == 0 {
			if err != nil {
				t.Fatalf("first RoundTrip err = %v, want nil", err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if got, _ := box.bytes(); string(got) != string(raw) || !box.ok {
				t.Fatalf("box = %s/%v, want the served body %s", got, box.ok, raw)
			}
			continue
		}
		if !errors.Is(err, errRawBodySlotTaken) {
			t.Fatalf("second RoundTrip err = %v, want errRawBodySlotTaken", err)
		}
		if resp != nil {
			t.Fatalf("second RoundTrip resp = %+v, want nil", resp)
		}
	}
}

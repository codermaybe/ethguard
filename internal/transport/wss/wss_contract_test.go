package wss

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// scriptedPeer is one in-process WebSocket endpoint speaking the owned codec
// from the server side. The handler owns the response script; requests are
// delivered in wire order.
type scriptedPeer struct {
	t        testing.TB
	conn     net.Conn
	frames   *serverFrames
	requests <-chan []byte
}

func newScriptedPeer(t testing.TB, serve func(peer *scriptedPeer)) string {
	t.Helper()
	requests := make(chan []byte, 32)
	srv := startRawServer(t, func(t testing.TB, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		writeUpgradeAccept(t, rw, r)
		frames := &serverFrames{t: t, conn: conn, br: rw.Reader}
		go func() {
			defer close(requests)
			reader := newFrameReader(rw.Reader, serverSide, 1<<20)
			for {
				frame, err := reader.readFrame()
				if err != nil {
					return
				}
				switch frame.opcode {
				case opClose:
					return
				case opText, opBinary:
					requests <- append([]byte(nil), frame.payload...)
				}
			}
		}()
		serve(&scriptedPeer{t: t, conn: conn, frames: frames, requests: requests})
	})
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func (p *scriptedPeer) writeFrame(frame []byte) {
	p.t.Helper()
	p.frames.write(true, opText, frame)
}

func (p *scriptedPeer) respond(request []byte, result string) {
	p.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, requestID(request), result)))
}

func (p *scriptedPeer) respondError(request []byte, code int, message, data string) {
	p.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q%s}}`, requestID(request), code, message, data)))
}

func requestID(request []byte) string {
	var wire struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(request, &wire)
	return string(wire.ID)
}

// pendingCount exposes the pending table to the package's contract tests.
func (t *Transport) pendingCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

func dialTestTransport(t testing.TB, url string, limit int64) *Transport {
	t.Helper()
	transportValue, err := Dialer{ReadLimit: limit}.DialContext(context.Background(), url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(transportValue.Close)
	return transportValue.(*Transport)
}

func TestCallDeliversOneRequestAndTheExactResult(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		var wire struct {
			Version string            `json:"jsonrpc"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
			ID      json.RawMessage   `json:"id"`
		}
		if err := json.Unmarshal(request, &wire); err != nil {
			t.Errorf("request is not JSON: %v", err)
			return
		}
		if wire.Version != "2.0" || wire.Method != "eth_getBalance" || len(wire.Params) != 2 {
			t.Errorf("request = %s, want a two-parameter eth_getBalance call", request)
		}
		if len(wire.ID) == 0 || string(wire.ID) == "null" {
			t.Errorf("request id missing: %s", request)
		}
		peer.respond(request, `"0x64"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	var raw json.RawMessage
	if err := tr.Call(context.Background(), "eth_getBalance", "0x1", "latest").Decode(&raw); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(raw) != `"0x64"` {
		t.Fatalf("result = %s, want the raw result bytes", raw)
	}
	if pending := tr.pendingCount(); pending != 0 {
		t.Fatalf("pending after a completed call = %d, want 0", pending)
	}
}

func TestResultTargetContract(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		for i := 0; i < 2; i++ {
			peer.respond(<-peer.requests, `{"a":1}`)
		}
	})
	tr := dialTestTransport(t, peerURL, 0)
	if err := tr.Call(context.Background(), "eth_test").Decode(nil); err != nil {
		t.Fatalf("nil result target: %v", err)
	}
	if err := tr.Call(context.Background(), "eth_test").Decode(map[string]any{}); err == nil {
		t.Fatal("non-pointer result target must fail")
	}
}

func TestProtocolErrorsKeepErrorDataPresence(t *testing.T) {
	cases := []struct {
		name string
		data string
		want rpc.ErrorDataPresenceState
		raw  string
	}{
		{name: "missing", data: ``, want: rpc.ErrorDataPresenceMissing},
		{name: "null", data: `,"data":null`, want: rpc.ErrorDataPresenceNull, raw: "null"},
		{name: "empty-data", data: `,"data":"0x"`, want: rpc.ErrorDataPresenceValue, raw: `"0x"`},
		{name: "concrete-data", data: `,"data":"0xdeadbeef"`, want: rpc.ErrorDataPresenceValue, raw: `"0xdeadbeef"`},
		{name: "object-data", data: `,"data":{"inner":1}`, want: rpc.ErrorDataPresenceValue, raw: `{"inner":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				peer.respondError(<-peer.requests, 3, "execution reverted", tc.data)
			})
			tr := dialTestTransport(t, peerURL, 0)
			var raw json.RawMessage
			err := tr.Call(context.Background(), "eth_call").Decode(&raw)
			var protocol rpc.ProtocolError
			if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 {
				t.Fatalf("err = %v, want the protocol error capability", err)
			}
			var dataErr rpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatalf("err = %v, lost the DataError capability", err)
			}
			var presence rpc.ErrorDataPresence
			if !errors.As(err, &presence) {
				t.Fatalf("err = %v, lost the presence capability", err)
			}
			rawData, present := presence.ErrorDataJSON()
			wanted := tc.want != rpc.ErrorDataPresenceMissing
			if present != wanted {
				t.Fatalf("present = %v, want %v", present, wanted)
			}
			if wanted && string(rawData) != tc.raw {
				t.Fatalf("raw data = %s, want %s", rawData, tc.raw)
			}
		})
	}
}

func TestMalformedFrameFailsTheCallAndKeepsTheConnection(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		first := <-peer.requests
		peer.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"ok"} trailing`, requestID(first))))
		second := <-peer.requests
		peer.respond(second, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	var raw json.RawMessage
	if err := tr.Call(context.Background(), "eth_test").Decode(&raw); !errors.Is(err, rpcwire.ErrMalformedResponse) {
		t.Fatalf("err = %v, want the malformed-response sentinel", err)
	}
	if err := tr.Call(context.Background(), "eth_test").Decode(&raw); err != nil {
		t.Fatalf("call after malformed frame: %v", err)
	}
	if string(raw) != `"ok"` {
		t.Fatalf("second result = %s, want the frame's result", raw)
	}
}

func TestOutOfOrderResponsesAreDemultiplexed(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		first := <-peer.requests
		second := <-peer.requests
		peer.respond(second, `"second"`)
		peer.respond(first, `"first"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	type outcome struct {
		value string
		err   error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var value string
			err := tr.Call(context.Background(), "eth_test").Decode(&value)
			results <- outcome{value: value, err: err}
		}()
	}
	seen := make(map[string]bool)
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("call failed: %v", result.err)
		}
		seen[result.value] = true
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("results = %v, want both answers", seen)
	}
}

func TestConcurrentCallsShareOneConnection(t *testing.T) {
	const parallel = 8
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		for i := 0; i < parallel; i++ {
			peer.respond(<-peer.requests, `"ok"`)
		}
	})
	tr := dialTestTransport(t, peerURL, 0)
	var wg sync.WaitGroup
	errs := make(chan error, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var value string
			if err := tr.Call(context.Background(), "eth_test").Decode(&value); err != nil {
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

func TestNotificationsAreIgnored(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame([]byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{}}`))
		peer.respond(request, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	var value string
	if err := tr.Call(context.Background(), "eth_test").Decode(&value); err != nil {
		t.Fatalf("call after notification: %v", err)
	}
	if value != "ok" {
		t.Fatalf("value = %q, want ok", value)
	}
}

func TestChainID(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		peer.respond(<-peer.requests, `"0x1"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	id, err := tr.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID: %v", err)
	}
	if id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("chain id = %s, want 1", id)
	}
}

func TestDialerRejectsNonWebSocketSchemes(t *testing.T) {
	for _, rawURL := range []string{"http://example.invalid", "https://example.invalid", "ipc:///tmp/x"} {
		if _, err := (Dialer{}).DialContext(context.Background(), rawURL); err == nil {
			t.Fatalf("scheme %q must be rejected", rawURL)
		}
	}
}

func TestCanceledContextSendsNoRequest(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		if method := requestMethod(request); method != "eth_followup" {
			t.Errorf("request method = %q, want only eth_followup", method)
		}
		peer.respond(request, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var value string
	if err := tr.Call(ctx, "eth_canceled").Decode(&value); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if pending := tr.pendingCount(); pending != 0 {
		t.Fatalf("pending after a rejected call = %d, want 0", pending)
	}
	if err := tr.Call(context.Background(), "eth_followup").Decode(&value); err != nil {
		t.Fatalf("call after cancellation: %v", err)
	}
}

func TestFailedWriteInvocationIsNeverReplayed(t *testing.T) {
	var connections atomic.Int32
	var requests atomic.Int32
	peerDone := make(chan struct{})
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		if connections.Add(1) != 1 {
			t.Error("failed invocation opened another connection")
			return
		}
		defer close(peerDone)
		request := <-peer.requests
		requests.Add(1)
		if method := requestMethod(request); method != "eth_sendRawTransaction" {
			t.Errorf("request method = %q, want eth_sendRawTransaction", method)
		}
		_ = peer.conn.Close()
		for range peer.requests {
			requests.Add(1)
		}
	})
	tr := dialTestTransport(t, peerURL, 0)
	var value string
	if err := tr.Call(context.Background(), "eth_sendRawTransaction", "0x01").Decode(&value); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("invocation err = %v, want connection closed", err)
	}
	if err := tr.Call(context.Background(), "eth_sendRawTransaction", "0x01").Decode(&value); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("post-failure invocation err = %v, want transport closed", err)
	}
	awaitLifecycleSignal(t, peerDone)
	if got := requests.Load(); got != 1 {
		t.Fatalf("server saw %d requests, want exactly one invocation", got)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("server saw %d connections, want no reconnect", got)
	}
}

// The adapter drives the existing pool seam without any pool change: chain
// identity probing and one collection travel over the same WebSocket.
func TestPoolCollectOverWSSAdapter(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		for request := range peer.requests {
			var wire struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(request, &wire)
			switch wire.Method {
			case "eth_chainId":
				peer.respond(request, `"0x1"`)
			case "eth_getBalance":
				peer.respond(request, `"0x64"`)
			default:
				peer.respondError(request, -32601, "method not found", "")
			}
		}
	})
	p, err := pool.NewWithMembers(1, []pool.Member{{Name: "ws", URL: peerURL}}, pool.WithDialer(Dialer{}))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer p.Close()
	emissions, err := p.Collect(context.Background(), "eth_getBalance", []any{"0x1", "latest"},
		pool.CollectOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want one per member", len(emissions))
	}
	if emissions[0].Response.Err() != nil {
		t.Fatalf("emission failed: %v", emissions[0].Response.Err())
	}
	if string(emissions[0].Response.Answer.Result) != `"0x64"` {
		t.Fatalf("emission value = %s, want the raw result", emissions[0].Response.Answer.Result)
	}
}

// A response frame whose request ID no pending call waits for — an ID the
// transport never issued, or a duplicate reply for an already-completed call
// — is dropped by the demultiplexer: it must not surface as a call result,
// must not be rerouted into a notification stream, and must leave the
// connection usable for the next real call. The single read loop processes
// frames in wire order, so once the following real call has completed, the
// ignored frame has already been dispatched.
func TestUnknownResponseIDIsIgnoredAndConnectionStaysUsable(t *testing.T) {
	t.Run("never-issued id", func(t *testing.T) {
		// The peer holds the connection open until the test releases it:
		// a serve function that returns mid-assertion closes the socket, and
		// the read loop's abort then terminates the stream — the documented
		// disconnect behavior, not a response-frame misroute.
		releasePeer := make(chan struct{})
		defer close(releasePeer)
		peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
			// Push a response for an ID the transport never registered,
			// before any call exists.
			peer.writeFrame([]byte(`{"jsonrpc":"2.0","id":4242,"result":"0xstray"}`))
			peer.respond(<-peer.requests, `"0x01"`)
			select {
			case <-peer.requests:
			case <-releasePeer:
			}
		})
		tr := dialTestTransport(t, peerURL, 0)
		stream, err := tr.OpenNotificationStream(context.Background(), "src", 1, 8)
		if err != nil {
			t.Fatalf("open stream: %v", err)
		}
		defer stream.Unsubscribe()

		var raw json.RawMessage
		if err := tr.Call(context.Background(), "eth_blockNumber").Decode(&raw); err != nil {
			t.Fatalf("call after stray response: %v", err)
		}
		if string(raw) != `"0x01"` {
			t.Fatalf("result = %s, want the real call's answer", raw)
		}
		if pending := tr.pendingCount(); pending != 0 {
			t.Fatalf("pending = %d, want 0", pending)
		}
		select {
		case notification := <-stream.Events():
			t.Fatalf("a response frame leaked into the notification stream: %+v", notification)
		default:
		}
	})

	t.Run("duplicate reply for a completed call", func(t *testing.T) {
		releasePeer := make(chan struct{})
		defer close(releasePeer)
		peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
			first := <-peer.requests
			peer.respond(first, `"first"`)
			// Re-send the same ID's answer after the call already consumed it.
			peer.respond(first, `"duplicate"`)
			peer.respond(<-peer.requests, `"second"`)
			select {
			case <-peer.requests:
			case <-releasePeer:
			}
		})
		tr := dialTestTransport(t, peerURL, 0)
		var first json.RawMessage
		if err := tr.Call(context.Background(), "eth_test").Decode(&first); err != nil {
			t.Fatalf("first call: %v", err)
		}
		if string(first) != `"first"` {
			t.Fatalf("first result = %s", first)
		}
		var second json.RawMessage
		if err := tr.Call(context.Background(), "eth_test").Decode(&second); err != nil {
			t.Fatalf("call after duplicate reply: %v", err)
		}
		if string(second) != `"second"` {
			t.Fatalf("second result = %s, want the second call's own answer", second)
		}
		if pending := tr.pendingCount(); pending != 0 {
			t.Fatalf("pending = %d, want 0", pending)
		}
	})
}

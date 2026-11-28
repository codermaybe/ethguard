package wss

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/transport"
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

func dialTestTransport(t testing.TB, url string, limit int64) *Transport {
	t.Helper()
	transportValue, err := Dialer{ReadLimit: limit}.DialContext(context.Background(), url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(transportValue.Close)
	return transportValue.(*Transport)
}

func TestDialerRejectsNonWebSocketSchemes(t *testing.T) {
	for _, rawURL := range []string{"http://example.invalid", "https://example.invalid", "ipc:///tmp/x"} {
		if _, err := (Dialer{}).DialContext(context.Background(), rawURL); err == nil {
			t.Fatalf("scheme %q must be rejected", rawURL)
		}
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

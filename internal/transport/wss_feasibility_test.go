package transport_test

// This file is a W1 feasibility prototype, not production code. It validates
// the mechanisms an EthGuard-owned WSS raw-call layer would depend on: gorilla
// framing with a bounded read, request-ID demultiplexing, cancellation and
// pending cleanup, close/disconnect behavior, and raw error-envelope
// presence. The prototype deliberately lives in a test so the production
// build stays unchanged until the evaluation is accepted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

type wssFrameResult struct {
	frame json.RawMessage
	err   error
}

// wssPrototype is the minimal call layer shape under evaluation: one
// connection, one reader, a pending map keyed by request ID, and lifecycle
// failures broadcast to every in-flight call.
type wssPrototype struct {
	conn *websocket.Conn

	writeMu sync.Mutex
	nextID  uint64

	mu      sync.Mutex
	pending map[string]chan wssFrameResult
	closed  bool
	readErr error

	done      chan struct{}
	closeOnce sync.Once
}

var _ transport.Transport = (*wssPrototype)(nil)

func dialWSSPrototype(ctx context.Context, rawURL string, readLimit int64) (*wssPrototype, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, _, err := dialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if readLimit > 0 {
		conn.SetReadLimit(readLimit)
	}
	p := &wssPrototype{
		conn:    conn,
		pending: make(map[string]chan wssFrameResult),
		done:    make(chan struct{}),
	}
	go p.readLoop()
	return p, nil
}

func (p *wssPrototype) readLoop() {
	for {
		_, frame, err := p.conn.ReadMessage()
		if err != nil {
			p.failAll(p.classifyReadError(err))
			return
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(frame, &envelope) != nil || len(envelope.ID) == 0 {
			// Notifications and undecodable frames have no pending caller.
			continue
		}
		id := string(envelope.ID)
		p.mu.Lock()
		waiter := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if waiter != nil {
			waiter <- wssFrameResult{frame: append(json.RawMessage(nil), frame...)}
		}
	}
}

func (p *wssPrototype) classifyReadError(err error) error {
	if errors.Is(err, websocket.ErrReadLimit) {
		return fmt.Errorf("wss: response frame exceeds the read limit: %w", transport.ErrBodyTooLarge)
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return transport.ErrConnectionClosed
	}
	return fmt.Errorf("%w: %v", transport.ErrConnectionClosed, err)
}

func (p *wssPrototype) failAll(err error) {
	p.mu.Lock()
	if p.readErr == nil {
		p.readErr = err
	}
	// The reader ended: the connection cannot be reused, so every later call
	// observes the lifecycle sentinel while the calls in flight keep the
	// specific cause.
	p.closed = true
	pending := p.pending
	p.pending = make(map[string]chan wssFrameResult)
	p.mu.Unlock()
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	for _, waiter := range pending {
		waiter <- wssFrameResult{err: err}
	}
}

func (p *wssPrototype) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return transport.ErrConnectionClosed
	}
	p.nextID++
	id := json.RawMessage(fmt.Sprintf("%d", p.nextID))
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	waiter := make(chan wssFrameResult, 1)
	p.pending[string(id)] = waiter
	done := p.done
	p.mu.Unlock()

	p.writeMu.Lock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = p.conn.SetWriteDeadline(deadline)
	} else {
		// A previous call may have set a deadline; each call owns its own.
		_ = p.conn.SetWriteDeadline(time.Time{})
	}
	writeErr := p.conn.WriteMessage(websocket.TextMessage, body)
	p.writeMu.Unlock()
	if writeErr != nil {
		p.removePending(string(id))
		return fmt.Errorf("%w: %v", transport.ErrConnectionClosed, writeErr)
	}

	select {
	case response := <-waiter:
		return p.complete(id, response, result)
	case <-ctx.Done():
		p.removePending(string(id))
		return ctx.Err()
	case <-done:
		// A response delivered just before the peer closed still wins: the
		// read loop may report the close before the caller is scheduled.
		select {
		case response := <-waiter:
			return p.complete(id, response, result)
		default:
		}
		p.mu.Lock()
		err := p.readErr
		p.mu.Unlock()
		if err == nil {
			err = transport.ErrConnectionClosed
		}
		return err
	}
}

func (p *wssPrototype) complete(id json.RawMessage, response wssFrameResult, result any) error {
	if response.err != nil {
		return response.err
	}
	decoded, err := rpcwire.DecodeResponse(response.frame, id)
	if err != nil {
		return err
	}
	if decoded.Error != nil {
		return decoded.Error
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(decoded.Result, result)
}

func (p *wssPrototype) removePending(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (p *wssPrototype) pendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

func (p *wssPrototype) ChainID(ctx context.Context) (*big.Int, error) {
	var raw string
	if err := p.CallContext(ctx, &raw, "eth_chainId"); err != nil {
		return nil, err
	}
	parsed, err := rpc.ParseHexQuantity(raw)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetUint64(parsed), nil
}

func (p *wssPrototype) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		_ = p.conn.Close()
	})
}

// --- scripted WebSocket server ---

type wssServer struct {
	*httptest.Server
	frames chan []byte
}

func newWSSServer(t *testing.T, handle func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte))) *wssServer {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	requests := make(chan []byte, 16)
	srv := &wssServer{frames: requests}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		go func() {
			for {
				_, frame, err := conn.ReadMessage()
				if err != nil {
					close(requests)
					return
				}
				requests <- append([]byte(nil), frame...)
			}
		}()
		handle(t, conn, requests, func(body []byte) {
			if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
				t.Errorf("server write: %v", err)
			}
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *wssServer) wsURL() string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

func requestIDOf(frame []byte) string {
	var wire struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(frame, &wire)
	return string(wire.ID)
}

func responseFor(frame []byte, result string) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, requestIDOf(frame), result))
}

// The call layer can demultiplex out-of-order responses by request ID.
func TestWSSPrototypeDemuxesOutOfOrderResponses(t *testing.T) {
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		first := <-requests
		second := <-requests
		// Answer the second request first: demux must not depend on order.
		respond(responseFor(second, `"second"`))
		respond(responseFor(first, `"first"`))
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	type outcome struct {
		value string
		err   error
	}
	results := make(chan outcome, 2)
	for _, arg := range []string{"a", "b"} {
		arg := arg
		go func() {
			var value string
			err := client.CallContext(context.Background(), &value, "eth_test", arg)
			results <- outcome{value: value, err: err}
		}()
	}
	got := make(map[string]bool)
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("call failed: %v", result.err)
		}
		got[result.value] = true
	}
	if !got["first"] || !got["second"] {
		t.Fatalf("demux results = %v, want both out-of-order answers", got)
	}
}

// Cancelling one call removes exactly that pending entry and leaves the
// connection usable for the next call.
func TestWSSPrototypeCancellationClearsPending(t *testing.T) {
	release := make(chan struct{})
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		first := <-requests
		<-release // hold the first response until after cancellation
		respond(responseFor(first, `"late"`))
		second := <-requests
		respond(responseFor(second, `"ok"`))
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var ignored string
	if err := client.CallContext(ctx, &ignored, "eth_test", "slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the call deadline", err)
	}
	if pending := client.pendingCount(); pending != 0 {
		t.Fatalf("pending calls after cancellation = %d, want 0", pending)
	}
	close(release)
	var value string
	if err := client.CallContext(context.Background(), &value, "eth_test", "next"); err != nil {
		t.Fatalf("next call after cancellation: %v", err)
	}
	if value != "ok" {
		t.Fatalf("next call value = %q, want ok", value)
	}
}

// An oversized frame is refused as a body-limit failure and the connection is
// no longer usable: a partial frame cannot be resynchronized.
func TestWSSPrototypeReadLimitFailsTheConnection(t *testing.T) {
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		<-requests
		respond([]byte(`"` + strings.Repeat("a", 256) + `"`))
		// Keep the handler alive until the client reacts.
		time.Sleep(100 * time.Millisecond)
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var value string
	if err := client.CallContext(context.Background(), &value, "eth_test"); !errors.Is(err, transport.ErrBodyTooLarge) {
		t.Fatalf("err = %v, want the neutral body-limit sentinel", err)
	}
	if err := client.CallContext(context.Background(), &value, "eth_test"); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("post-limit err = %v, want the closed sentinel", err)
	}
}

// Close fails every in-flight call and blocks new ones.
func TestWSSPrototypeCloseFailsPendingCalls(t *testing.T) {
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		<-requests
		time.Sleep(300 * time.Millisecond)
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var value string
		done <- client.CallContext(context.Background(), &value, "eth_test")
	}()
	time.Sleep(50 * time.Millisecond)
	client.Close()
	select {
	case err := <-done:
		if !errors.Is(err, transport.ErrConnectionClosed) {
			t.Fatalf("in-flight err = %v, want the closed sentinel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight call did not fail on Close")
	}
	var value string
	if err := client.CallContext(context.Background(), &value, "eth_test"); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("post-close err = %v, want the closed sentinel", err)
	}
}

// A peer disconnect fails the pending call instead of hanging it.
func TestWSSPrototypeDisconnectFailsPendingCalls(t *testing.T) {
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		<-requests
		conn.Close()
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var value string
	err = client.CallContext(context.Background(), &value, "eth_test")
	if !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("disconnect err = %v, want the closed sentinel", err)
	}
}

// The raw error envelope survives the self-built layer, so missing, null, and
// concrete data are distinguishable before any method policy sees them.
func TestWSSPrototypePreservesErrorDataPresence(t *testing.T) {
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
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
				req := <-requests
				respond([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":3,"message":"execution reverted"%s}}`, requestIDOf(req), tc.data)))
			})
			client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var value string
			err = client.CallContext(context.Background(), &value, "eth_call")
			var protocol rpc.ProtocolError
			if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 {
				t.Fatalf("err = %v, want the protocol error capability", err)
			}
			var presence rpc.ErrorDataPresence
			if !errors.As(err, &presence) {
				t.Fatalf("err = %v, lost the presence capability", err)
			}
			raw, present := presence.ErrorDataJSON()
			wanted := tc.want != rpc.ErrorDataPresenceMissing
			if present != wanted {
				t.Fatalf("present = %v, want %v", present, wanted)
			}
			if wanted && string(raw) != tc.raw {
				t.Fatalf("raw data = %s, want %s", raw, tc.raw)
			}
		})
	}
}

// JSON-RPC notifications (no id) must not disturb the pending map.
func TestWSSPrototypeIgnoresNotifications(t *testing.T) {
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		req := <-requests
		respond([]byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{}}`))
		respond(responseFor(req, `"ok"`))
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var value string
	if err := client.CallContext(context.Background(), &value, "eth_test"); err != nil {
		t.Fatalf("call after notification: %v", err)
	}
	if value != "ok" {
		t.Fatalf("value = %q, want ok", value)
	}
}

// The prototype must also support the chain-identity probe the transport seam
// requires.
func TestWSSPrototypeChainID(t *testing.T) {
	server := newWSSServer(t, func(t *testing.T, conn *websocket.Conn, requests <-chan []byte, respond func([]byte)) {
		req := <-requests
		respond(responseFor(req, `"0x1"`))
	})
	client, err := dialWSSPrototype(context.Background(), server.wsURL(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	id, err := client.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID: %v", err)
	}
	if id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("chain id = %s, want 1", id)
	}
}

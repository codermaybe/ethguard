package wss

// Dual-implementation differential verification (wire contract W6). The same
// scripted fixtures run through the owned transport and through a gorilla-
// backed comparison adapter — a faithful copy of the pre-replacement adapter
// kept only inside this file as the differential oracle. The comparison is
// over observable contract outcomes: raw result bytes, protocol error
// code/message, error-data presence, close/read-limit/write-failure
// classification, pending lifecycle, and cancellation/deadline behavior.
// Error TEXTS are deliberately not compared: gorilla's own strings are an L3
// diagnostic difference. Everything else must match, and a diff may only be
// resolved by explaining it, never by loosening an upper-layer test.
//
// This file and the oracle are transitional: W7 removes both together with
// the gorilla dependency.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// compatTransport is the gorilla-backed comparison implementation of the
// transport seam, mirroring the pending table, demux, and classification the
// adapter had before the owned wire stack replaced them.
type compatTransport struct {
	conn  *websocket.Conn
	ids   *rpcwire.IDGen
	limit int64

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan frameResult
	closing bool
	closed  bool

	socketOnce sync.Once
}

func dialCompat(t *testing.T, url string, limit int64) *compatTransport {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("compat dial: %v", err)
	}
	if limit > 0 {
		conn.SetReadLimit(limit)
	}
	c := &compatTransport{conn: conn, ids: rpcwire.NewIDGen(), limit: limit, pending: make(map[string]chan frameResult)}
	go c.readLoop()
	t.Cleanup(c.Close)
	return c
}

func (c *compatTransport) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed || c.closing
	c.mu.Unlock()
	if closed {
		return ErrTransportClosed
	}
	id := c.ids.Next()
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		return fmt.Errorf("wss: encode request: %w", err)
	}
	waiter := make(chan frameResult, 1)
	c.mu.Lock()
	if c.closed || c.closing {
		c.mu.Unlock()
		return ErrTransportClosed
	}
	c.pending[string(id)] = waiter
	c.mu.Unlock()

	if err := c.write(ctx, body); err != nil {
		c.removePending(string(id))
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return err
	}

	select {
	case response := <-waiter:
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return c.complete(id, response, result)
	case <-ctx.Done():
		c.removePending(string(id))
		return ctx.Err()
	}
}

func (c *compatTransport) write(ctx context.Context, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	} else {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		writeErr := fmt.Errorf("wss: write request: %w", transport.ErrConnectionClosed)
		c.abort(writeErr)
		return writeErr
	}
	return nil
}

func (c *compatTransport) complete(id json.RawMessage, response frameResult, result any) error {
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
	return decodeResult(decoded.Result, result)
}

func (c *compatTransport) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	closeSocket := c.closing && !c.closed && len(c.pending) == 0
	if closeSocket {
		c.closed = true
	}
	c.mu.Unlock()
	if closeSocket {
		c.closeSocket()
	}
}

func (c *compatTransport) readLoop() {
	for {
		_, frame, err := c.conn.ReadMessage()
		if err != nil {
			c.abort(c.classifyReadError(err))
			return
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.NewDecoder(strings.NewReader(string(frame))).Decode(&envelope) != nil || len(envelope.ID) == 0 {
			continue
		}
		id := string(envelope.ID)
		c.mu.Lock()
		waiter := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if waiter != nil {
			waiter <- frameResult{frame: append(json.RawMessage(nil), frame...)}
			c.finishCloseAfterPending()
		}
	}
}

func (c *compatTransport) classifyReadError(err error) error {
	if errors.Is(err, websocket.ErrReadLimit) {
		return fmt.Errorf("wss: inbound message exceeds limit %d: %w", c.limit, errors.Join(transport.ErrBodyTooLarge, transport.ErrConnectionClosed))
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrTransportClosed
	}
	return fmt.Errorf("wss: connection lost: %v: %w", err, transport.ErrConnectionClosed)
}

func (c *compatTransport) abort(err error) {
	c.mu.Lock()
	c.closed = true
	c.closing = true
	pending := c.pending
	c.pending = make(map[string]chan frameResult)
	c.mu.Unlock()
	c.closeSocket()
	for _, waiter := range pending {
		waiter <- frameResult{err: err}
	}
}

func (c *compatTransport) finishCloseAfterPending() {
	c.mu.Lock()
	closeSocket := c.closing && !c.closed && len(c.pending) == 0
	if closeSocket {
		c.closed = true
	}
	c.mu.Unlock()
	if closeSocket {
		c.closeSocket()
	}
}

func (c *compatTransport) closeSocket() {
	c.socketOnce.Do(func() { _ = c.conn.Close() })
}

func (c *compatTransport) pendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// Close rejects new calls and releases the socket once pending calls end,
// matching the owned adapter's close contract.
func (c *compatTransport) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closing = true
	closeSocket := len(c.pending) == 0
	if closeSocket {
		c.closed = true
	}
	c.mu.Unlock()
	if closeSocket {
		c.closeSocket()
	}
}

// differentialTransport is the common shape the scenarios drive.
type differentialTransport interface {
	CallContext(ctx context.Context, result any, method string, args ...any) error
	pendingCount() int
}

// dialDifferential binds one implementation to a fresh scripted peer.
func dialDifferential(t *testing.T, owned bool, peerURL string, limit int64) differentialTransport {
	t.Helper()
	if owned {
		return dialTestTransport(t, peerURL, limit)
	}
	return dialCompat(t, peerURL, limit)
}

// differentialOutcome is the observable contract fingerprint of a call.
type differentialOutcome struct {
	resultJSON string
	errKind    string
	code       int
	message    string
	presence   string
	rawData    string
	pending    int
}

// classifyError reduces an error to the classification the upper layers see.
// Sentinel order matters: ErrTransportClosed wraps ErrConnectionClosed.
func classifyError(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, transport.ErrBodyTooLarge):
		return "body-too-large"
	case errors.Is(err, ErrTransportClosed):
		return "transport-closed"
	case errors.Is(err, transport.ErrConnectionClosed):
		return "connection-closed"
	case errors.Is(err, rpcwire.ErrMalformedResponse):
		return "malformed-response"
	case errors.Is(err, rpcwire.ErrIDMismatch):
		return "id-mismatch"
	}
	var protocol rpc.ProtocolError
	if errors.As(err, &protocol) {
		return "protocol"
	}
	return "other:" + err.Error()
}

func captureCall(ctx context.Context, tr differentialTransport, result any, method string, args ...any) differentialOutcome {
	out := differentialOutcome{}
	err := tr.CallContext(ctx, result, method, args...)
	out.errKind = classifyError(err)
	switch {
	case err == nil:
		if raw, ok := result.(*json.RawMessage); ok {
			out.resultJSON = string(*raw)
		}
	default:
		var protocol rpc.ProtocolError
		if errors.As(err, &protocol) {
			out.code = protocol.ErrorCode()
			out.message = protocol.Error()
		}
		var presence rpc.ErrorDataPresence
		if errors.As(err, &presence) {
			raw, present := presence.ErrorDataJSON()
			switch {
			case !present:
				out.presence = "missing"
			case string(raw) == "null":
				out.presence = "null"
				out.rawData = string(raw)
			default:
				out.presence = "value"
				out.rawData = string(raw)
			}
		}
	}
	out.pending = tr.pendingCount()
	return out
}

func compareOutcomes(t *testing.T, name string, owned, compat differentialOutcome) {
	t.Helper()
	if owned.errKind != compat.errKind {
		t.Errorf("%s: classification owned=%q compat=%q", name, owned.errKind, compat.errKind)
	}
	if owned.resultJSON != compat.resultJSON {
		t.Errorf("%s: result bytes owned=%q compat=%q", name, owned.resultJSON, compat.resultJSON)
	}
	if owned.code != compat.code {
		t.Errorf("%s: protocol code owned=%d compat=%d", name, owned.code, compat.code)
	}
	if owned.message != compat.message {
		t.Errorf("%s: protocol message owned=%q compat=%q", name, owned.message, compat.message)
	}
	if owned.presence != compat.presence {
		t.Errorf("%s: data presence owned=%q compat=%q", name, owned.presence, compat.presence)
	}
	if owned.rawData != compat.rawData {
		t.Errorf("%s: raw data bytes owned=%q compat=%q", name, owned.rawData, compat.rawData)
	}
	if owned.pending != compat.pending {
		t.Errorf("%s: pending owned=%d compat=%d", name, owned.pending, compat.pending)
	}
}

// diffScript scripts the peer; diffScenario drives one implementation and
// returns the observable fingerprint.
type (
	diffScript   = func(peer *scriptedPeer)
	diffScenario = func(t *testing.T, tr differentialTransport) differentialOutcome
)

// differentialScenario runs one scenario against both implementations on
// independent scripted peers and compares the fingerprints. newRun is invoked
// once per implementation so every run gets its own coordination state.
func differentialScenario(t *testing.T, name string, limit int64, newRun func() (diffScript, diffScenario)) {
	t.Helper()
	var owned, compat differentialOutcome
	t.Run(name, func(t *testing.T) {
		t.Run("owned", func(t *testing.T) {
			script, scenario := newRun()
			peerURL := newScriptedPeer(t, script)
			tr := dialDifferential(t, true, peerURL, limit)
			owned = scenario(t, tr)
		})
		t.Run("compat", func(t *testing.T) {
			script, scenario := newRun()
			peerURL := newScriptedPeer(t, script)
			tr := dialDifferential(t, false, peerURL, limit)
			compat = scenario(t, tr)
		})
		compareOutcomes(t, name, owned, compat)
	})
}

func TestDifferentialRawCallParity(t *testing.T) {
	cases := []struct {
		name   string
		result string
	}{
		{name: "hex quantity", result: `"0x64"`},
		{name: "null result", result: `null`},
		{name: "object result", result: `{"blockNumber":"0x1","hash":"0xabc"}`},
		{name: "array result", result: `["0x1","0x2"]`},
		{name: "bool result", result: `true`},
		{name: "empty object", result: `{}`},
	}
	for _, tc := range cases {
		tc := tc
		differentialScenario(t, "raw/"+tc.name, 0,
			func() (diffScript, diffScenario) {
				script := func(peer *scriptedPeer) {
					peer.respond(<-peer.requests, tc.result)
				}
				scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
					var raw json.RawMessage
					return captureCall(context.Background(), tr, &raw, "eth_probe")
				}
				return script, scenario
			})
	}
}

func TestDifferentialProtocolErrorParity(t *testing.T) {
	cases := []struct {
		name string
		code int
		msg  string
		data string
	}{
		{name: "missing-data", code: -32601, msg: "method not found", data: ``},
		{name: "null-data", code: 3, msg: "execution reverted", data: `,"data":null`},
		{name: "empty-data", code: 3, msg: "execution reverted", data: `,"data":"0x"`},
		{name: "revert-data", code: 3, msg: "execution reverted", data: `,"data":"0x08c379a0"`},
		{name: "object-data", code: -32000, msg: "rate limited", data: `,"data":{"retry":1}`},
		{name: "rate-limit-code", code: -32005, msg: "limit exceeded", data: ``},
	}
	for _, tc := range cases {
		tc := tc
		differentialScenario(t, "error/"+tc.name, 0,
			func() (diffScript, diffScenario) {
				script := func(peer *scriptedPeer) {
					peer.respondError(<-peer.requests, tc.code, tc.msg, tc.data)
				}
				scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
					var raw json.RawMessage
					return captureCall(context.Background(), tr, &raw, "eth_probe")
				}
				return script, scenario
			})
	}
}

func TestDifferentialMalformedFrameParity(t *testing.T) {
	differentialScenario(t, "malformed frame fails one call and keeps the connection", 0,
		func() (diffScript, diffScenario) {
			script := func(peer *scriptedPeer) {
				first := <-peer.requests
				peer.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"ok"} trailing`, requestID(first))))
				peer.respond(<-peer.requests, `"second"`)
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				var raw json.RawMessage
				first := captureCall(context.Background(), tr, &raw, "eth_probe")
				second := captureCall(context.Background(), tr, &raw, "eth_probe")
				if second.errKind != "none" || second.resultJSON != `"second"` {
					t.Errorf("follow-up call after malformed frame = %+v", second)
				}
				return first
			}
			return script, scenario
		})
}

func TestDifferentialOutOfOrderDemuxParity(t *testing.T) {
	differentialScenario(t, "out-of-order demux", 0,
		func() (diffScript, diffScenario) {
			script := func(peer *scriptedPeer) {
				first := <-peer.requests
				second := <-peer.requests
				peer.respond(second, `"second"`)
				peer.respond(first, `"first"`)
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				type outcome struct {
					value string
					err   error
				}
				results := make(chan outcome, 2)
				for i := 0; i < 2; i++ {
					go func() {
						var value string
						err := tr.CallContext(context.Background(), &value, "eth_probe")
						results <- outcome{value: value, err: err}
					}()
				}
				seen := map[string]bool{}
				failed := ""
				for i := 0; i < 2; i++ {
					got := <-results
					if got.err != nil {
						failed = got.err.Error()
					}
					seen[got.value] = true
				}
				if failed != "" || !seen["first"] || !seen["second"] {
					t.Errorf("demux = %v %q", seen, failed)
				}
				return differentialOutcome{errKind: "none", pending: tr.pendingCount()}
			}
			return script, scenario
		})
}

func TestDifferentialReadLimitParity(t *testing.T) {
	differentialScenario(t, "read limit", 64,
		func() (diffScript, diffScenario) {
			script := func(peer *scriptedPeer) {
				peer.respond(<-peer.requests, `"`+strings.Repeat("a", 256)+`"`)
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				var raw json.RawMessage
				first := captureCall(context.Background(), tr, &raw, "eth_probe")
				if first.errKind != "body-too-large" {
					t.Errorf("errKind = %q, want body-too-large", first.errKind)
				}
				var follow json.RawMessage
				if err := tr.CallContext(context.Background(), &follow, "eth_probe"); !errors.Is(err, ErrTransportClosed) {
					t.Errorf("post-limit call = %v, want transport closed", err)
				}
				return first
			}
			return script, scenario
		})
}

func TestDifferentialPeerDisconnectParity(t *testing.T) {
	differentialScenario(t, "peer disconnect", 0,
		func() (diffScript, diffScenario) {
			script := func(peer *scriptedPeer) {
				<-peer.requests
				_ = peer.conn.Close()
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				var raw json.RawMessage
				first := captureCall(context.Background(), tr, &raw, "eth_probe")
				if first.errKind != "connection-closed" {
					t.Errorf("errKind = %q, want connection-closed", first.errKind)
				}
				if err := tr.CallContext(context.Background(), &raw, "eth_probe"); !errors.Is(err, ErrTransportClosed) {
					t.Errorf("post-disconnect call = %v, want transport closed", err)
				}
				return first
			}
			return script, scenario
		})
}

func TestDifferentialWriteFailureNeverReplaysParity(t *testing.T) {
	differentialScenario(t, "write failure never replays", 0,
		func() (diffScript, diffScenario) {
			requests := 0
			peerDone := make(chan struct{})
			script := func(peer *scriptedPeer) {
				defer close(peerDone)
				<-peer.requests
				requests++
				_ = peer.conn.Close()
				for range peer.requests {
				}
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				var raw json.RawMessage
				first := captureCall(context.Background(), tr, &raw, "eth_sendRawTransaction", "0x01")
				if first.errKind != "connection-closed" {
					t.Errorf("errKind = %q, want connection-closed", first.errKind)
				}
				second := captureCall(context.Background(), tr, &raw, "eth_sendRawTransaction", "0x01")
				if second.errKind != "transport-closed" {
					t.Errorf("post-failure errKind = %q, want transport-closed", second.errKind)
				}
				select {
				case <-peerDone:
				case <-time.After(3 * time.Second):
					t.Fatal("peer script did not finish")
				}
				if requests != 1 {
					t.Errorf("server saw %d requests, want exactly one invocation (no replay)", requests)
				}
				return first
			}
			return script, scenario
		})
}

func TestDifferentialCancellationParity(t *testing.T) {
	differentialScenario(t, "cancellation keeps the connection", 0,
		func() (diffScript, diffScenario) {
			received := make(chan struct{})
			release := make(chan struct{})
			script := func(peer *scriptedPeer) {
				first := <-peer.requests
				close(received)
				<-release
				peer.respond(first, `"late"`)
				peer.respond(<-peer.requests, `"ok"`)
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var value string
				callDone := make(chan error, 1)
				go func() { callDone <- tr.CallContext(ctx, &value, "eth_cancel") }()
				<-received
				cancel()
				if err := <-callDone; !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
				deadline := time.Now().Add(3 * time.Second)
				for tr.pendingCount() != 0 {
					if time.Now().After(deadline) {
						t.Fatal("canceled call left the pending table")
					}
					time.Sleep(time.Millisecond)
				}
				close(release)
				var follow json.RawMessage
				if err := tr.CallContext(context.Background(), &follow, "eth_followup"); err != nil || string(follow) != `"ok"` {
					t.Errorf("follow-up after cancellation = %q %v, want the follow-up result", follow, err)
				}
				return differentialOutcome{errKind: "canceled", pending: tr.pendingCount()}
			}
			return script, scenario
		})
}

func TestDifferentialDeadlineParity(t *testing.T) {
	differentialScenario(t, "deadline bounds a hanging call", 0,
		func() (diffScript, diffScenario) {
			script := func(peer *scriptedPeer) {
				first := <-peer.requests
				time.Sleep(50 * time.Millisecond)
				peer.respond(first, `"late"`)
				peer.respond(<-peer.requests, `"ok"`)
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				var raw json.RawMessage
				first := captureCall(ctx, tr, &raw, "eth_probe")
				if first.errKind != "deadline" {
					t.Errorf("errKind = %q, want deadline", first.errKind)
				}
				var follow json.RawMessage
				if err := tr.CallContext(context.Background(), &follow, "eth_probe"); err != nil || string(follow) != `"ok"` {
					t.Errorf("follow-up after deadline = %q %v", follow, err)
				}
				return first
			}
			return script, scenario
		})
}

func TestDifferentialPeerCloseFrameParity(t *testing.T) {
	differentialScenario(t, "peer close frame", 0,
		func() (diffScript, diffScenario) {
			script := func(peer *scriptedPeer) {
				<-peer.requests
				if err := peer.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(3*time.Second)); err != nil {
					t.Errorf("peer close: %v", err)
				}
				time.Sleep(100 * time.Millisecond)
			}
			scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
				var raw json.RawMessage
				first := captureCall(context.Background(), tr, &raw, "eth_probe")
				if first.errKind != "connection-closed" {
					t.Errorf("errKind = %q, want connection-closed", first.errKind)
				}
				var follow json.RawMessage
				if err := tr.CallContext(context.Background(), &follow, "eth_probe"); !errors.Is(err, ErrTransportClosed) {
					t.Errorf("post-close call = %v, want transport closed", err)
				}
				return first
			}
			return script, scenario
		})
}

// TestDifferentialRandomizedResultParity feeds seeded-random JSON payloads
// through both stacks and requires byte parity on every one.
func TestDifferentialRandomizedResultParity(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	kinds := []func() string{
		func() string { return `"0x` + fmt.Sprintf("%x", random.Int63()) + `"` },
		func() string { return fmt.Sprintf(`%d`, random.Int63()) },
		func() string { return `null` },
		func() string { return `true` },
		func() string { return `false` },
		func() string {
			return fmt.Sprintf(`{"n":%d,"s":"v%d","arr":[%d,%d]}`, random.Intn(1000), random.Intn(100), random.Intn(10), random.Intn(10))
		},
	}
	for i := 0; i < 40; i++ {
		payload := kinds[random.Intn(len(kinds))]()
		differentialScenario(t, fmt.Sprintf("randomized/%d", i), 0,
			func() (diffScript, diffScenario) {
				script := func(peer *scriptedPeer) {
					peer.respond(<-peer.requests, payload)
				}
				scenario := func(t *testing.T, tr differentialTransport) differentialOutcome {
					var raw json.RawMessage
					return captureCall(context.Background(), tr, &raw, "eth_probe")
				}
				return script, scenario
			})
	}
}

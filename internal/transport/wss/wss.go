// Package wss implements EthGuard's minimal WebSocket JSON-RPC transport
// adapter. It is a raw-call connection: one long-lived socket, request-ID
// demultiplexing, bounded inbound frames, per-call cancellation, and lifecycle
// cleanup. It deliberately has no subscription, filter, batch, reconnect, or
// write-path replay surface.
//
// The WebSocket wire itself — HTTP Upgrade handshake, frame codec, and
// connection lifecycle — is EthGuard-owned (websocket_handshake.go,
// websocket_frame.go, websocket_connection.go) and implemented on the Go
// standard library only; see docs/design/wss-wire-contract.md.
//
// Response frames are validated by internal/rpcwire, the same project-owned
// codec the native HTTP adapter uses, so protocol errors keep the exact
// error-data presence (missing, null, value) the wire carried. This adapter
// does not change any adjudication semantics: method policy, quorum,
// reference, and typed projection stay above the Transport seam.
package wss

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/limits"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// DefaultReadLimit bounds one inbound message. It matches the audited HTTP
// response bound so no layer admits a body another layer would reject purely
// on size.
const DefaultReadLimit = limits.MaxBodyBytes

const defaultHandshakeTimeout = 10 * time.Second

// ErrTransportClosed is returned for calls on a closed or broken transport.
// It wraps the neutral lifecycle sentinel so core packages classify the
// lifecycle without importing this adapter.
var ErrTransportClosed = fmt.Errorf("wss: transport closed: %w", transport.ErrConnectionClosed)

// Dialer dials one long-lived WebSocket JSON-RPC connection for ws and wss
// URLs.
type Dialer struct {
	// ReadLimit bounds one inbound frame. Zero selects DefaultReadLimit.
	ReadLimit int64
	// HandshakeTimeout bounds the WebSocket handshake. Zero selects the
	// audited default.
	HandshakeTimeout time.Duration
}

// NewDialer returns the raw WSS adapter factory. The composition root selects
// it only for sources explicitly configured with transport: wss.
func NewDialer() Dialer { return Dialer{} }

// DialContext implements transport.Dialer. The connection is established
// once; calls share it for the transport's lifetime. No automatic reconnect
// is attempted: a broken connection fails its pending calls and rejects new
// ones (the Geth reconnect semantics are a separate contract).
func (d Dialer) DialContext(ctx context.Context, rawURL string) (transport.Transport, error) {
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("wss: parse endpoint: %w", err)
	}
	switch endpoint.Scheme {
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("wss: no known transport for URL scheme %q", endpoint.Scheme)
	}
	handshake := d.HandshakeTimeout
	if handshake <= 0 {
		handshake = defaultHandshakeTimeout
	}
	result, err := dialWebSocket(ctx, rawURL, dialOptions{handshakeTimeout: handshake})
	if err != nil {
		return nil, fmt.Errorf("wss: dial: %w", err)
	}
	limit := d.ReadLimit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	t := &Transport{
		ws:      newConn(result, limit),
		ids:     rpcwire.NewIDGen(),
		limit:   limit,
		pending: make(map[string]chan frameResult),
	}
	go t.readLoop()
	return t, nil
}

type frameResult struct {
	frame json.RawMessage
	err   error
}

// Transport is one WSS JSON-RPC connection. One reader goroutine decodes
// inbound frames; calls are safe for concurrent use.
type Transport struct {
	ws    *Conn
	ids   *rpcwire.IDGen
	limit int64

	mu      sync.Mutex
	pending map[string]chan frameResult
	closing bool
	closed  bool

	socketOnce sync.Once
}

var _ transport.Transport = (*Transport)(nil)
var _ transport.Dialer = Dialer{}

// CallContext performs one JSON-RPC call over the shared socket. The returned
// error preserves the wire shape: protocol errors implement rpc.ProtocolError,
// rpc.DataError, and rpc.ErrorDataPresence; transport failures map onto the
// neutral sentinels.
func (t *Transport) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	closed := t.closed || t.closing
	t.mu.Unlock()
	if closed {
		return ErrTransportClosed
	}
	id := t.ids.Next()
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		return fmt.Errorf("wss: encode request: %w", err)
	}
	waiter := make(chan frameResult, 1)
	t.mu.Lock()
	if t.closed || t.closing {
		t.mu.Unlock()
		return ErrTransportClosed
	}
	t.pending[string(id)] = waiter
	t.mu.Unlock()

	if err := t.write(ctx, body); err != nil {
		t.removePending(string(id))
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
		return t.complete(id, response, result)
	case <-ctx.Done():
		// A cancelled call owns only its pending entry; the connection stays
		// usable for every other call.
		t.removePending(string(id))
		return ctx.Err()
	}
}

// write sends one request frame under the single-writer lock. Cancellation
// while waiting for that lock must not send a frame or poison the socket —
// the connection layer rechecks the context before any byte moves and
// returns the context error untouched, which is the one write outcome that
// leaves a healthy socket. Once a frame write starts, any write failure
// makes the socket unusable: a partially written frame can never be
// resynchronized. The caller's context deadline bounds the write; a call
// without a deadline clears any deadline a previous call left behind.
func (t *Transport) write(ctx context.Context, body []byte) error {
	err := t.ws.WriteMessage(ctx, body)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return ctxErr
	}
	writeErr := fmt.Errorf("wss: write request: %w", transport.ErrConnectionClosed)
	t.abort(writeErr)
	return writeErr
}

func (t *Transport) complete(id json.RawMessage, response frameResult, result any) error {
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

func (t *Transport) removePending(id string) {
	t.mu.Lock()
	delete(t.pending, id)
	closeSocket := t.closing && !t.closed && len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	if closeSocket {
		t.closeSocket()
	}
}

// readLoop owns the socket's read side. A delivered frame is matched to its
// pending call by request ID; notifications and unknown IDs are ignored. Any
// read failure ends the connection: a partial or oversized frame cannot be
// resynchronized, so every pending call fails and new calls are rejected.
//
// The ID is read with a streaming decoder so a frame carrying a valid prefix
// and trailing garbage still reaches its caller; rpcwire, not the demux,
// owns the malformed-response classification.
func (t *Transport) readLoop() {
	for {
		message, err := t.ws.ReadMessage()
		if err != nil {
			t.abort(t.classifyReadError(err))
			return
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.NewDecoder(bytes.NewReader(message)).Decode(&envelope) != nil || len(envelope.ID) == 0 {
			continue
		}
		id := string(envelope.ID)
		t.mu.Lock()
		waiter := t.pending[id]
		delete(t.pending, id)
		t.mu.Unlock()
		if waiter != nil {
			waiter <- frameResult{frame: append(json.RawMessage(nil), message...)}
			t.finishCloseAfterPending()
		}
	}
}

func (t *Transport) classifyReadError(err error) error {
	if errors.Is(err, ErrFrameTooLarge) {
		return fmt.Errorf("wss: inbound message exceeds limit %d: %w", t.limit, errors.Join(transport.ErrBodyTooLarge, transport.ErrConnectionClosed))
	}
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return ErrTransportClosed
	}
	return fmt.Errorf("wss: connection lost: %w: %w", err, transport.ErrConnectionClosed)
}

// abort marks the connection unusable and fails every pending call with the
// same cause. It is the single fan-out point for read limits, peer
// disconnects, and write failures. Closing the socket here also unblocks a
// reader still waiting on a broken write path, so the goroutine cannot leak.
func (t *Transport) abort(err error) {
	t.mu.Lock()
	t.closed = true
	t.closing = true
	pending := t.pending
	t.pending = make(map[string]chan frameResult)
	t.mu.Unlock()
	t.closeSocket()
	for _, waiter := range pending {
		waiter <- frameResult{err: err}
	}
}

// finishCloseAfterPending closes a socket after Close was requested and the
// last in-flight call has received its response. Close does not cancel calls;
// each call's context owns its own deadline and cancellation.
func (t *Transport) finishCloseAfterPending() {
	t.mu.Lock()
	closeSocket := t.closing && !t.closed && len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	if closeSocket {
		t.closeSocket()
	}
}

func (t *Transport) closeSocket() {
	t.socketOnce.Do(func() { _ = t.ws.Close() })
}

// ChainID implements transport.Transport through a plain eth_chainId call.
func (t *Transport) ChainID(ctx context.Context) (*big.Int, error) {
	var raw string
	if err := t.CallContext(ctx, &raw, "eth_chainId"); err != nil {
		return nil, err
	}
	value, err := rpc.ParseHexQuantity(raw)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetUint64(value), nil
}

// Close blocks new calls and releases the socket after all in-flight calls
// finish or cancel. In-flight calls remain governed by their own contexts,
// matching the transport Port contract; a peer failure can still abort them
// immediately. Close is idempotent.
func (t *Transport) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closing = true
	closeSocket := len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	if closeSocket {
		t.closeSocket()
	}
}

// decodeResult fills the caller's result target from the raw result bytes.
func decodeResult(raw json.RawMessage, result any) error {
	switch target := result.(type) {
	case nil:
		return nil
	case *json.RawMessage:
		*target = append((*target)[:0], raw...)
		return nil
	default:
		if reflect.ValueOf(result).Kind() != reflect.Pointer {
			return fmt.Errorf("wss: result must be pointer or nil, got %T", result)
		}
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("wss: decode result: %w", err)
		}
		return nil
	}
}

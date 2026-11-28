// Package wss implements EthGuard's minimal WebSocket JSON-RPC transport
// adapter. It is a raw-call connection with an opt-in bounded notification
// subscription capability. The shared streamrpc owner handles request-ID
// demultiplexing, per-call cancellation, notification budgets and lifecycle.
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
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/codermaybe/ethguard/internal/limits"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/streamrpc"
)

// DefaultReadLimit bounds one inbound message. It matches the audited HTTP
// response bound so no layer admits a body another layer would reject purely
// on size.
const DefaultReadLimit = limits.MaxBodyBytes

const defaultHandshakeTimeout = 10 * time.Second

// ErrTransportClosed is returned for calls on a closed or broken transport.
// It wraps the neutral lifecycle sentinel so core packages classify the
// lifecycle without importing this adapter.
var ErrTransportClosed = streamrpc.ErrTransportClosed

// ErrNotificationBackpressure identifies a bounded notification queue
// overflow without changing unary transport errors.
var ErrNotificationBackpressure = streamrpc.ErrNotificationBackpressure

// Dialer dials one long-lived WebSocket JSON-RPC connection for ws and wss
// URLs.
type Dialer struct {
	// ReadLimit bounds one inbound frame. Zero selects DefaultReadLimit.
	ReadLimit int64
	// HandshakeTimeout bounds the WebSocket handshake. Zero selects the
	// audited default.
	HandshakeTimeout time.Duration
	// PendingMessages/PendingBytes bound all pre-bind notifications on this
	// connection. Zero selects the finite transport defaults.
	PendingMessages int
	PendingBytes    uint64
}

// NewDialer returns the raw WSS adapter factory. The composition root selects
// it for ws(s) endpoints, including those with an inferred transport.
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
	return &Transport{Transport: streamrpc.New(newConn(result, limit), streamrpc.Options{
		PendingMessages: d.PendingMessages, PendingBytes: d.PendingBytes,
	})}, nil
}

// Transport binds the shared duplex RPC owner to the WebSocket wire.
type Transport struct{ *streamrpc.Transport }
type Notification = transport.Notification
type NotificationStream = streamrpc.NotificationStream
type Subscription = streamrpc.Subscription

var _ transport.Transport = (*Transport)(nil)
var _ transport.Dialer = Dialer{}

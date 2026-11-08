// Package wss implements EthGuard's minimal WebSocket JSON-RPC transport
// adapter. It is a raw-call connection with an opt-in bounded notification
// subscription capability. Unary calls retain their request-ID demultiplexing,
// per-call cancellation, and lifecycle cleanup; subscription delivery is owned
// by NotificationStream and the separate subscription state machine.
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

const (
	// maxPendingNotificationsPerID bounds the buffered notifications for one
	// not-yet-bound subscription ID.
	maxPendingNotificationsPerID = 256
	// maxPendingSubscriptionIDs bounds how many distinct never-bound
	// subscription IDs may hold a pending buffer at once, so a hostile or
	// chatty peer cannot grow the pending map without limit.
	maxPendingSubscriptionIDs = 256
)

// ErrTransportClosed is returned for calls on a closed or broken transport.
// It wraps the neutral lifecycle sentinel so core packages classify the
// lifecycle without importing this adapter.
var ErrTransportClosed = fmt.Errorf("wss: transport closed: %w", transport.ErrConnectionClosed)

// ErrNotificationBackpressure identifies a bounded notification queue
// overflow without changing unary transport errors.
var ErrNotificationBackpressure = errors.New("wss: notification backpressure")

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
		ws:                   newConn(result, limit),
		ids:                  rpcwire.NewIDGen(),
		limit:                limit,
		pending:              make(map[string]chan frameResult),
		streams:              make(map[*NotificationStream]struct{}),
		subscriptionIDs:      make(map[string]*NotificationStream),
		pendingNotifications: make(map[string][]Notification),
	}
	go t.readLoop()
	return t, nil
}

type frameResult struct {
	frame json.RawMessage
	err   error
}

// Notification is the transport-neutral subscription notification. The raw
// result is copied before delivery so callers cannot retain the frame buffer.
type Notification = transport.Notification

// NotificationStream is an opt-in notification channel for a single WSS
// source session. It is separate from Transport's unary call surface;
// connections without a stream continue to ignore notifications.
type NotificationStream struct {
	owner          *Transport
	sourceID       string
	generation     uint64
	subscriptionID string
	events         chan Notification
	errs           chan error
	done           chan struct{}

	mu     sync.Mutex
	closed bool
	once   sync.Once
}

const pendingSubscriptionID = "\x00pending-subscription"

// Events returns the bounded notification queue.
func (s *NotificationStream) Events() <-chan Notification { return s.events }

// Err returns the asynchronous terminal error channel. At most one error is
// delivered; normal Unsubscribe and owner Close close it without an error.
func (s *NotificationStream) Err() <-chan error { return s.errs }

// Unsubscribe is idempotent and stops delivery of new notifications.
func (s *NotificationStream) Unsubscribe() { s.finish(nil) }

// Close is an alias for Unsubscribe for internal lifecycle owners.
func (s *NotificationStream) Close() { s.Unsubscribe() }

func (s *NotificationStream) finish(err error) {
	s.once.Do(func() {
		s.owner.removeNotificationStream(s)
		s.mu.Lock()
		s.closed = true
		close(s.done)
		close(s.events)
		if err != nil {
			s.errs <- err
		}
		close(s.errs)
		s.mu.Unlock()
	})
}

func (s *NotificationStream) deliver(notification Notification) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return true
	}
	if s.subscriptionID != "" && notification.SubscriptionID != s.subscriptionID {
		return true
	}
	select {
	case s.events <- notification:
		return true
	default:
		return false
	}
}

func (s *NotificationStream) bindSubscriptionID(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.subscriptionID = id
	s.owner.mu.Lock()
	if s.owner.subscriptionIDs == nil {
		s.owner.subscriptionIDs = make(map[string]*NotificationStream)
	}
	s.owner.subscriptionIDs[id] = s
	pending := s.owner.pendingNotifications[id]
	s.owner.removePendingNotificationsLocked(id)
	s.owner.mu.Unlock()
	for _, notification := range pending {
		notification.SourceID = s.sourceID
		notification.Generation = s.generation
		notification.ReceivedAt = time.Now()
		select {
		case s.events <- notification:
		default:
			return false
		}
	}
	return true
}

// Subscription is the WSS-side handle returned by Subscribe. It keeps the
// notification stream and the server-side subscription ID together so
// Unsubscribe can be idempotent and always releases both resources.
type Subscription struct {
	transport *Transport
	stream    *NotificationStream
	id        string
	once      sync.Once
}

func (s *Subscription) Events() <-chan Notification { return s.stream.Events() }
func (s *Subscription) Err() <-chan error           { return s.stream.Err() }

func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		// Best effort: the stream is always closed locally even when the
		// server-side unsubscribe cannot be delivered because the socket died.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ok bool
		if s.transport != nil {
			_ = s.transport.CallContext(ctx, &ok, "eth_unsubscribe", s.id)
		}
		s.stream.Unsubscribe()
	})
}

// Subscribe opens an eth namespace subscription and binds its notifications
// to a bounded stream. It is intentionally outside the unary Transport
// interface; callers must opt into the WSS capability explicitly.
func (t *Transport) Subscribe(ctx context.Context, kind string, args ...any) (*Subscription, error) {
	if kind == "" {
		return nil, fmt.Errorf("wss: empty subscription kind")
	}
	// The setup context bounds eth_subscribe only. The stream remains alive
	// until Unsubscribe, owner Close, or a transport failure. It registers
	// with the pending placeholder from birth, so no other subscription's
	// notification can land here during the eth_subscribe round trip; the
	// first server push for this subscription (which can outrun the
	// response) buffers at the transport level and is flushed on bind.
	stream, err := t.openNotificationStreamWithID(context.WithoutCancel(ctx), "", 0, 256, pendingSubscriptionID)
	if err != nil {
		return nil, err
	}
	var id string
	params := make([]any, 0, len(args)+1)
	params = append(params, kind)
	params = append(params, args...)
	if err := t.CallContext(ctx, &id, "eth_subscribe", params...); err != nil {
		stream.Unsubscribe()
		return nil, err
	}
	if id == "" {
		stream.Unsubscribe()
		return nil, fmt.Errorf("wss: eth_subscribe returned empty id")
	}
	if !stream.bindSubscriptionID(id) {
		stream.finish(ErrNotificationBackpressure)
		return nil, ErrNotificationBackpressure
	}
	return &Subscription{transport: t, stream: stream, id: id}, nil
}

// SubscribeCapability adapts the concrete WSS subscription to the optional
// transport capability without widening the unary Transport interface.
func (t *Transport) SubscribeCapability(ctx context.Context, kind string, args ...any) (transport.Subscription, error) {
	return t.Subscribe(ctx, kind, args...)
}

// Transport is one WSS JSON-RPC connection. One reader goroutine decodes
// inbound frames; calls are safe for concurrent use.
type Transport struct {
	ws    *Conn
	ids   *rpcwire.IDGen
	limit int64

	mu                       sync.Mutex
	pending                  map[string]chan frameResult
	streams                  map[*NotificationStream]struct{}
	subscriptionIDs          map[string]*NotificationStream
	pendingNotifications     map[string][]Notification
	pendingNotificationOrder []string
	closing                  bool
	closed                   bool

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

// OpenNotificationStream opts this raw WSS connection into subscription
// notifications. The stream is bounded; a full queue terminates only that
// stream with a backpressure error and never blocks the unary reader.
//
// Until a caller binds a subscription ID, the stream carries the generic ""
// marker and receives every notification the peer pushes — the documented
// direct-open semantics. The Subscribe path instead registers through
// openNotificationStreamWithID so its stream never passes through the
// generic marker: between registration and the eth_subscribe answer,
// dispatch must not route other subscriptions' notifications into it.
func (t *Transport) OpenNotificationStream(ctx context.Context, sourceID string, generation uint64, buffer int) (*NotificationStream, error) {
	return t.openNotificationStreamWithID(ctx, sourceID, generation, buffer, "")
}

func (t *Transport) openNotificationStreamWithID(ctx context.Context, sourceID string, generation uint64, buffer int, initialID string) (*NotificationStream, error) {
	if ctx == nil {
		return nil, fmt.Errorf("wss: nil notification context")
	}
	if buffer <= 0 {
		return nil, fmt.Errorf("wss: notification buffer must be positive")
	}
	stream := &NotificationStream{
		owner:          t,
		sourceID:       sourceID,
		generation:     generation,
		subscriptionID: initialID,
		events:         make(chan Notification, buffer),
		errs:           make(chan error, 1),
		done:           make(chan struct{}),
	}
	t.mu.Lock()
	if t.closed || t.closing {
		t.mu.Unlock()
		return nil, ErrTransportClosed
	}
	if t.streams == nil {
		t.streams = make(map[*NotificationStream]struct{})
	}
	if t.subscriptionIDs == nil {
		t.subscriptionIDs = make(map[string]*NotificationStream)
	}
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			stream.finish(ctx.Err())
		case <-stream.done:
		}
	}()
	return stream, nil
}

func (t *Transport) removeNotificationStream(stream *NotificationStream) {
	t.mu.Lock()
	delete(t.streams, stream)
	if stream.subscriptionID != "" && t.subscriptionIDs[stream.subscriptionID] == stream {
		delete(t.subscriptionIDs, stream.subscriptionID)
		// A server that keeps pushing after the unsubscribe must not pin a
		// pending buffer here forever; the entry dies with the binding.
		t.removePendingNotificationsLocked(stream.subscriptionID)
	}
	t.mu.Unlock()
}

func (t *Transport) removePendingNotificationsLocked(id string) {
	delete(t.pendingNotifications, id)
	for i, pendingID := range t.pendingNotificationOrder {
		if pendingID == id {
			t.pendingNotificationOrder = append(t.pendingNotificationOrder[:i], t.pendingNotificationOrder[i+1:]...)
			return
		}
	}
}

func (t *Transport) notificationStreams() []*NotificationStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	streams := make([]*NotificationStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	return streams
}

func (t *Transport) dispatchNotification(message []byte) {
	notification, err := decodeNotification(message)
	if err != nil {
		for _, stream := range t.notificationStreams() {
			stream.finish(fmt.Errorf("wss: notification: %w", err))
		}
		return
	}

	t.mu.Lock()
	if t.closing || t.closed {
		t.mu.Unlock()
		return
	}
	bound := t.subscriptionIDs[notification.SubscriptionID]
	streams := make([]*NotificationStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	if bound == nil {
		_, exists := t.pendingNotifications[notification.SubscriptionID]
		if !exists && len(t.pendingNotifications) >= maxPendingSubscriptionIDs {
			// Retire the oldest never-bound ID instead of silently dropping
			// the first event for a newly created subscription. The map stays
			// bounded while a successful later bind can still recover its
			// event that outran the eth_subscribe response.
			oldest := t.pendingNotificationOrder[0]
			t.removePendingNotificationsLocked(oldest)
		}
		pending := t.pendingNotifications[notification.SubscriptionID]
		if len(pending) < maxPendingNotificationsPerID {
			if len(pending) == 0 {
				t.pendingNotificationOrder = append(t.pendingNotificationOrder, notification.SubscriptionID)
			}
			t.pendingNotifications[notification.SubscriptionID] = append(pending, notification)
		}
	}
	t.mu.Unlock()

	if bound != nil {
		t.deliverNotification(bound, notification)
	}
	for _, stream := range streams {
		if stream == bound {
			continue
		}
		stream.mu.Lock()
		generic := stream.subscriptionID == ""
		stream.mu.Unlock()
		if generic {
			t.deliverNotification(stream, notification)
		}
	}
}

func (t *Transport) deliverNotification(stream *NotificationStream, notification Notification) {
	notification.SourceID = stream.sourceID
	notification.Generation = stream.generation
	notification.ReceivedAt = time.Now()
	if !stream.deliver(notification) {
		stream.finish(ErrNotificationBackpressure)
	}
}

func decodeNotification(message []byte) (Notification, error) {
	var envelope struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return Notification{}, err
	}
	if envelope.Method != "eth_subscription" {
		return Notification{}, fmt.Errorf("unsupported notification method %q", envelope.Method)
	}
	var params struct {
		Subscription string          `json:"subscription"`
		Result       json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return Notification{}, err
	}
	if params.Subscription == "" || len(params.Result) == 0 {
		return Notification{}, fmt.Errorf("subscription notification missing subscription or result")
	}
	return Notification{
		Method:         envelope.Method,
		SubscriptionID: params.Subscription,
		Result:         append(json.RawMessage(nil), params.Result...),
		Raw:            append(json.RawMessage(nil), message...),
	}, nil
}

// write sends one request frame under the single-writer lock. Cancellation
// while waiting for that lock must not send a frame or poison the socket —
// the connection layer rechecks the context before any byte moves and
// returns the context error itself, which is the one write outcome that
// leaves a healthy socket. Identity, not errors.Is: an i/o deadline failure
// during the write must keep aborting the connection (a partially written
// frame can never be resynchronized) even if a future standard library makes
// os.ErrDeadlineExceeded match context.DeadlineExceeded. The caller's
// context deadline bounds the write; a call without a deadline clears any
// deadline a previous call left behind.
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
			t.dispatchNotification(message)
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
	t.pendingNotifications = make(map[string][]Notification)
	t.pendingNotificationOrder = nil
	streams := t.streams
	t.streams = make(map[*NotificationStream]struct{})
	t.mu.Unlock()
	t.closeSocket()
	for stream := range streams {
		stream.finish(err)
	}
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
	streams := t.streams
	t.streams = make(map[*NotificationStream]struct{})
	t.pendingNotifications = make(map[string][]Notification)
	t.pendingNotificationOrder = nil
	closeSocket := len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	for stream := range streams {
		stream.finish(nil)
	}
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

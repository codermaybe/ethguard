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

// ErrNotificationBackpressure identifies a bounded notification queue
// overflow without changing unary transport errors.
var ErrNotificationBackpressure = fmt.Errorf("wss: %w", transport.ErrNotificationBackpressure)

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
		pendingMessagesLimit: d.PendingMessages, pendingBytesLimit: d.PendingBytes,
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

	mu          sync.Mutex
	closed      bool
	once        sync.Once
	policy      transport.NotificationPolicy
	first, last *notificationItem
	queued      int
	queuedBytes uint64
	notify      chan struct{}
	abort       chan struct{}
	pumpDone    chan struct{}
	terminal    error
}

type notificationItem struct {
	value Notification
	bytes uint64
	next  *notificationItem
}

const defaultNotificationBytes uint64 = 8 << 20
const defaultPendingMessages = 256
const defaultPendingBytes uint64 = 8 << 20

const pendingSubscriptionID = "\x00pending-subscription"

// Events returns the bounded notification queue.
func (s *NotificationStream) Events() <-chan Notification { return s.events }

// Err returns the asynchronous terminal error channel. At most one error is
// delivered; normal Unsubscribe and owner Close close it without an error.
func (s *NotificationStream) Err() <-chan error { return s.errs }

// Unsubscribe is idempotent and stops delivery of new notifications.
func (s *NotificationStream) Unsubscribe() {
	s.finish(nil)
	if s.pumpDone != nil {
		<-s.pumpDone
	}
}

// Close is an alias for Unsubscribe for internal lifecycle owners.
func (s *NotificationStream) Close() { s.Unsubscribe() }

func (s *NotificationStream) finish(err error) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.terminal = err
		close(s.done)
	}
	if err == nil {
		s.terminal = nil
		s.once.Do(func() { close(s.abort) })
		s.dropLocked()
	}
	s.mu.Unlock()
	s.signal()
}

func (s *NotificationStream) deliver(notification Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.subscriptionID != "" && notification.SubscriptionID != s.subscriptionID {
		return nil
	}
	return s.enqueueLocked(notification)
}

func notificationBytes(n Notification) uint64 {
	return uint64(cap(n.Raw)+cap(n.Result)+len(n.SubscriptionID)+len(n.Method)+len(n.SourceID)) + 256
}
func (s *NotificationStream) enqueueLocked(n Notification) error {
	bytes := notificationBytes(n)
	if uint64(len(n.Raw)) > s.policy.MaxMessageBytes {
		return transport.ErrBodyTooLarge
	}
	if s.queued >= s.policy.Messages || bytes > s.policy.Bytes || s.queuedBytes > s.policy.Bytes-bytes {
		return ErrNotificationBackpressure
	}
	release := func() {}
	if s.policy.Reserve != nil {
		var err error
		release, err = s.policy.Reserve(bytes)
		if err != nil {
			return err
		}
	}
	n.Release = sync.OnceFunc(release)
	item := &notificationItem{value: n, bytes: bytes}
	if s.last == nil {
		s.first = item
	} else {
		s.last.next = item
	}
	s.last = item
	s.queued++
	s.queuedBytes += bytes
	s.signal()
	return nil
}
func (s *NotificationStream) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
func (s *NotificationStream) dropLocked() {
	for s.first != nil {
		n := s.first
		s.first = n.next
		if n.value.Release != nil {
			n.value.Release()
		}
		n.value = Notification{}
		n.next = nil
		s.queued--
		s.queuedBytes -= n.bytes
	}
	s.last = nil
}
func (s *NotificationStream) pump() {
	defer close(s.pumpDone)
	defer close(s.events)
	defer close(s.errs)
	defer s.owner.removeNotificationStream(s)
	for {
		s.mu.Lock()
		item := s.first
		var value Notification
		if item != nil {
			value = item.value
			s.first = item.next
			if s.first == nil {
				s.last = nil
			}
			item.next = nil
		}
		if item == nil && s.closed {
			err := s.terminal
			s.mu.Unlock()
			if err != nil {
				s.errs <- err
			}
			return
		}
		s.mu.Unlock()
		if item == nil {
			select {
			case <-s.notify:
				continue
			case <-s.abort:
				return
			}
		}
		select {
		case s.events <- value:
			s.mu.Lock()
			s.queued--
			s.queuedBytes -= item.bytes
			item.next = nil
			item.value = Notification{}
			s.mu.Unlock()
		case <-s.abort:
			if value.Release != nil {
				value.Release()
			}
			s.mu.Lock()
			s.queued--
			s.queuedBytes -= item.bytes
			s.mu.Unlock()
			return
		}
	}
}

func (s *NotificationStream) bindSubscriptionID(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.owner.mu.Lock()
	s.subscriptionID = id
	if s.owner.setupStreams[s] {
		s.owner.mu.Unlock()
		return false
	}
	delete(s.owner.setupStreams, s)
	if s.owner.subscriptionIDs == nil {
		s.owner.subscriptionIDs = make(map[string]*NotificationStream)
	}
	s.owner.subscriptionIDs[id] = s
	pending := s.owner.pendingNotifications[id]
	for _, notification := range pending {
		notification.SourceID = s.sourceID
		notification.Generation = s.generation
		notification.ReceivedAt = time.Now()
		if err := s.enqueueLocked(notification); err != nil {
			s.owner.removePendingNotificationsLocked(id)
			s.owner.mu.Unlock()
			s.closed = true
			s.terminal = err
			close(s.done)
			s.signal()
			return false
		}
	}
	s.owner.removePendingNotificationsLocked(id)
	if len(s.owner.setupStreams) == 0 {
		s.owner.clearPendingNotificationsLocked()
	}
	s.owner.mu.Unlock()
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
		s.stream.Unsubscribe()
		// Best effort: the stream is always closed locally even when the
		// server-side unsubscribe cannot be delivered because the socket died.
		if s.transport != nil {
			s.transport.unsubscribeServer(s.id)
		}
	})
}

func (t *Transport) unsubscribeServer(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	_ = t.Call(ctx, "eth_unsubscribe", id).Decode(&ok)
}

// Subscribe opens an eth namespace subscription and binds its notifications
// to a bounded stream. It is intentionally outside the unary Transport
// interface; callers must opt into the WSS capability explicitly.
func (t *Transport) Subscribe(ctx context.Context, kind string, args ...any) (*Subscription, error) {
	return t.subscribe(ctx, transport.NotificationPolicy{Messages: 256, Bytes: defaultNotificationBytes, MaxMessageBytes: defaultNotificationBytes}, kind, args...)
}
func (t *Transport) SubscribeBounded(ctx context.Context, p transport.NotificationPolicy, kind string, args ...any) (transport.Subscription, error) {
	return t.subscribe(ctx, p, kind, args...)
}
func (t *Transport) subscribe(ctx context.Context, policy transport.NotificationPolicy, kind string, args ...any) (*Subscription, error) {
	if kind == "" {
		return nil, fmt.Errorf("wss: empty subscription kind")
	}
	// The setup context bounds eth_subscribe only. The stream remains alive
	// until Unsubscribe, owner Close, or a transport failure. It registers
	// with the pending placeholder from birth, so no other subscription's
	// notification can land here during the eth_subscribe round trip; the
	// first server push for this subscription (which can outrun the
	// response) buffers at the transport level and is flushed on bind.
	stream, err := t.openNotificationStreamPolicy(context.WithoutCancel(ctx), "", 0, policy, pendingSubscriptionID)
	if err != nil {
		return nil, err
	}
	var id string
	params := make([]any, 0, len(args)+1)
	params = append(params, kind)
	params = append(params, args...)
	response, requestWritten := t.call(ctx, "eth_subscribe", params...)
	if err := response.Decode(&id); err != nil {
		stream.Unsubscribe()
		if ctx.Err() != nil {
			err = t.cancelSubscribeSetup(response, requestWritten, err)
		}
		return nil, err
	}
	if id == "" {
		stream.Unsubscribe()
		return nil, fmt.Errorf("wss: eth_subscribe returned empty id")
	}
	if !stream.bindSubscriptionID(id) {
		stream.mu.Lock()
		cause := stream.terminal
		stream.mu.Unlock()
		stream.Unsubscribe()
		t.unsubscribeServer(id)
		if cause == nil {
			cause = ErrNotificationBackpressure
		}
		return nil, cause
	}
	return &Subscription{transport: t, stream: stream, id: id}, nil
}

// cancelSubscribeSetup retires only resources that the canceled call could
// have created. The response can retain an associated answer even when Decode
// returns the exchange's cancellation; consume that answer without replacing
// its communication facts.
func (t *Transport) cancelSubscribeSetup(response rpc.Response, requestWritten bool, cause error) error {
	if !requestWritten || response.Answer.Kind == rpc.AnswerProtocolError {
		return cause
	}
	if response.Answer.Kind == rpc.AnswerResult {
		var id string
		if err := json.Unmarshal(response.Answer.Result, &id); err == nil && id != "" {
			t.unsubscribeServer(id)
			return cause
		}
	}
	// Once written, an unanswered request may have created a server-side
	// subscription with an unknown ID. Retiring the socket releases it.
	err := fmt.Errorf("wss: subscribe setup: %w: %w", cause, transport.ErrConnectionClosed)
	t.abort(err)
	return err
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

	mu                                    sync.Mutex
	pending                               map[string]chan frameResult
	streams                               map[*NotificationStream]struct{}
	subscriptionIDs                       map[string]*NotificationStream
	pendingNotifications                  map[string][]Notification
	pendingNotificationOrder              []string
	setupStreams                          map[*NotificationStream]bool
	pendingMessagesLimit, pendingMessages int
	pendingBytesLimit, pendingBytes       uint64
	closing                               bool
	closed                                bool

	socketOnce sync.Once
}

var _ transport.Transport = (*Transport)(nil)
var _ transport.Dialer = Dialer{}

// Call performs one unary exchange on the shared socket. Only a frame
// associated with this request can advance it beyond Started.
func (t *Transport) Call(ctx context.Context, method string, args ...any) rpc.Response {
	response, _ := t.call(ctx, method, args...)
	return response
}

// call also reports successful request-frame completion for subscription
// setup cleanup. ExchangeStarted alone cannot supply this fact: execution
// starts before acquiring the writer, and does not promise delivery.
func (t *Transport) call(ctx context.Context, method string, args ...any) (rpc.Response, bool) {
	exchange := rpc.Exchange{}
	failed := func(err error) rpc.Response {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		exchange.Cause = err
		return rpc.Response{Exchange: exchange}
	}
	if err := ctx.Err(); err != nil {
		return failed(err), false
	}
	t.mu.Lock()
	closed := t.closed || t.closing
	t.mu.Unlock()
	if closed {
		return failed(ErrTransportClosed), false
	}
	id := t.ids.Next()
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		return failed(fmt.Errorf("wss: encode request: %w", err)), false
	}
	waiter := make(chan frameResult, 1)
	t.mu.Lock()
	if t.closed || t.closing {
		t.mu.Unlock()
		return failed(ErrTransportClosed), false
	}
	t.pending[string(id)] = waiter
	t.mu.Unlock()
	exchange.Progress = rpc.ExchangeStarted
	if err := t.write(ctx, body); err != nil {
		t.removePending(string(id))
		return failed(err), false
	}
	complete := func(frame frameResult) rpc.Response {
		if frame.err != nil {
			return failed(frame.err)
		}
		exchange.Progress = rpc.ExchangeCompleted
		exchange.Cause = ctx.Err()
		return rpcwire.CompleteCall(frame.frame, id, exchange)
	}
	select {
	case frame := <-waiter:
		return complete(frame), true
	case <-ctx.Done():
		// Synchronize removal with read-loop association. An already associated
		// frame remains a fact even when cancellation wins final error delivery.
		t.mu.Lock()
		delete(t.pending, string(id))
		select {
		case frame := <-waiter:
			t.mu.Unlock()
			return complete(frame), true
		default:
			t.mu.Unlock()
			t.drainCloseIfSettled()
			return failed(ctx.Err()), true
		}
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
	return t.openNotificationStreamPolicy(ctx, sourceID, generation, transport.NotificationPolicy{Messages: buffer, Bytes: defaultNotificationBytes, MaxMessageBytes: defaultNotificationBytes}, initialID)
}
func (t *Transport) openNotificationStreamPolicy(ctx context.Context, sourceID string, generation uint64, policy transport.NotificationPolicy, initialID string) (*NotificationStream, error) {
	if ctx == nil {
		return nil, fmt.Errorf("wss: nil notification context")
	}
	if policy.Messages <= 0 || policy.MaxMessageBytes == 0 || policy.Bytes < policy.MaxMessageBytes {
		return nil, fmt.Errorf("wss: positive notification limits required")
	}
	stream := &NotificationStream{
		owner:          t,
		sourceID:       sourceID,
		generation:     generation,
		subscriptionID: initialID,
		events:         make(chan Notification),
		errs:           make(chan error, 1),
		done:           make(chan struct{}),
		policy:         policy, notify: make(chan struct{}, 1), abort: make(chan struct{}), pumpDone: make(chan struct{}),
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
	if initialID == pendingSubscriptionID {
		if t.setupStreams == nil {
			t.setupStreams = make(map[*NotificationStream]bool)
		}
		t.setupStreams[stream] = false
	}
	if t.pendingMessagesLimit <= 0 {
		t.pendingMessagesLimit = defaultPendingMessages
	}
	if t.pendingBytesLimit == 0 {
		t.pendingBytesLimit = defaultPendingBytes
	}
	t.mu.Unlock()
	go stream.pump()
	go func() {
		select {
		case <-ctx.Done():
			stream.Unsubscribe()
		case <-stream.pumpDone:
		}
	}()
	return stream, nil
}

func (t *Transport) removeNotificationStream(stream *NotificationStream) {
	t.mu.Lock()
	delete(t.streams, stream)
	delete(t.setupStreams, stream)
	if len(t.setupStreams) == 0 {
		t.clearPendingNotificationsLocked()
	}
	if stream.subscriptionID != "" && t.subscriptionIDs[stream.subscriptionID] == stream {
		delete(t.subscriptionIDs, stream.subscriptionID)
		// A server that keeps pushing after the unsubscribe must not pin a
		// pending buffer here forever; the entry dies with the binding.
		t.removePendingNotificationsLocked(stream.subscriptionID)
	}
	t.mu.Unlock()
}

func (t *Transport) removePendingNotificationsLocked(id string) {
	for _, n := range t.pendingNotifications[id] {
		t.pendingMessages--
		t.pendingBytes -= notificationBytes(n)
	}
	delete(t.pendingNotifications, id)
	for i, pendingID := range t.pendingNotificationOrder {
		if pendingID == id {
			copy(t.pendingNotificationOrder[i:], t.pendingNotificationOrder[i+1:])
			t.pendingNotificationOrder[len(t.pendingNotificationOrder)-1] = ""
			t.pendingNotificationOrder = t.pendingNotificationOrder[:len(t.pendingNotificationOrder)-1]
			return
		}
	}
}
func (t *Transport) clearPendingNotificationsLocked() {
	t.pendingNotifications = make(map[string][]Notification)
	t.pendingNotificationOrder = nil
	t.pendingMessages = 0
	t.pendingBytes = 0
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
	var failed []*NotificationStream
	if bound == nil && len(t.setupStreams) > 0 {
		bytes := notificationBytes(notification)
		if t.pendingMessages >= t.pendingMessagesLimit || bytes > t.pendingBytesLimit || t.pendingBytes > t.pendingBytesLimit-bytes {
			for stream := range t.setupStreams {
				t.setupStreams[stream] = true
				failed = append(failed, stream)
			}
			t.clearPendingNotificationsLocked()
		} else {
			pending := t.pendingNotifications[notification.SubscriptionID]
			if len(pending) == 0 {
				t.pendingNotificationOrder = append(t.pendingNotificationOrder, notification.SubscriptionID)
			}
			t.pendingNotifications[notification.SubscriptionID] = append(pending, notification)
			t.pendingMessages++
			t.pendingBytes += bytes
		}
	}
	t.mu.Unlock()
	for _, stream := range failed {
		stream.finish(ErrNotificationBackpressure)
	}

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
	if err := stream.deliver(notification); err != nil {
		stream.finish(err)
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
		Result:         params.Result,
		Raw:            message,
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
	writeErr := fmt.Errorf("wss: write request: %w: %w", err, transport.ErrConnectionClosed)
	t.abort(writeErr)
	return writeErr
}

// drainCloseIfSettled marks the transport closed and closes the socket when
// a requested close has drained its last pending response. The drain
// condition is the one Close-vs-pending rule both the delivered-response and
// the close paths apply.
func (t *Transport) drainCloseIfSettled() {
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

func (t *Transport) removePending(id string) {
	t.mu.Lock()
	delete(t.pending, id)
	t.mu.Unlock()
	t.drainCloseIfSettled()
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
		if waiter != nil {
			waiter <- frameResult{frame: message}
		}
		t.mu.Unlock()
		if waiter != nil {
			// The frame codec assembled this message into a fresh buffer that
			// nobody else holds: the envelope's id is consumed before the
			// handover, notifications decode into their own copies, and the
			// pending entry was deleted above so this is the only delivery.
			// Ownership transfers to the waiter, which reads the frame once
			// inside rpcwire.CompleteCall.
			t.drainCloseIfSettled()
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
	t.clearPendingNotificationsLocked()
	streams := make([]*NotificationStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()
	t.closeSocket()
	for _, stream := range streams {
		stream.finish(err)
	}
	for _, waiter := range pending {
		waiter <- frameResult{err: err}
	}
}

func (t *Transport) closeSocket() {
	t.socketOnce.Do(func() { _ = t.ws.Close() })
}

// ChainID implements transport.Transport through a plain eth_chainId call.
func (t *Transport) ChainID(ctx context.Context) (*big.Int, error) {
	var raw string
	if err := t.Call(ctx, "eth_chainId").Decode(&raw); err != nil {
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
	t.closing = true
	streams := make([]*NotificationStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.clearPendingNotificationsLocked()
	closeSocket := len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	for _, stream := range streams {
		stream.Unsubscribe()
	}
	if closeSocket {
		t.closeSocket()
	}
}

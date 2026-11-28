package streamrpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
)

// Notification is the transport-neutral subscription notification. The raw
// result is decoded into independent bytes; Raw retains its owned message.
type Notification = transport.Notification

// NotificationStream is an opt-in notification channel for a single duplex
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
	if !s.owner.hasLiveSetupLocked() {
		s.owner.clearPendingNotificationsLocked()
	}
	s.owner.mu.Unlock()
	return true
}

// OpenNotificationStream opts this raw duplex connection into subscription
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
		return nil, fmt.Errorf("streamrpc: nil notification context")
	}
	if policy.Messages <= 0 || policy.MaxMessageBytes == 0 || policy.Bytes < policy.MaxMessageBytes {
		return nil, fmt.Errorf("streamrpc: positive notification limits required")
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
	if !t.hasLiveSetupLocked() {
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

// Budget-failed setups remain registered until their pumps exit. Their
// failure marker prevents binding and must also prevent pending retention.
func (t *Transport) hasLiveSetupLocked() bool {
	for _, failed := range t.setupStreams {
		if !failed {
			return true
		}
	}
	return false
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
			stream.finish(fmt.Errorf("streamrpc: notification: %w", err))
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
	if bound == nil && t.hasLiveSetupLocked() {
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
	notification, err := rpcwire.DecodeSubscriptionNotification(message)
	if err != nil {
		return Notification{}, err
	}
	return Notification{
		Method:         notification.Method,
		SubscriptionID: notification.SubscriptionID,
		Result:         notification.Result,
		Raw:            message,
	}, nil
}

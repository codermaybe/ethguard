package wss

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcwire"
)

func newPendingTestTransport(t *testing.T) *Transport {
	t.Helper()
	conn, _ := newPipeConn(t)
	tr := &Transport{
		ws:                   conn,
		ids:                  rpcwire.NewIDGen(),
		limit:                DefaultReadLimit,
		pending:              make(map[string]chan frameResult),
		streams:              make(map[*NotificationStream]struct{}),
		subscriptionIDs:      make(map[string]*NotificationStream),
		pendingNotifications: make(map[string][]Notification),
	}
	return tr
}

func pushNotification(t *testing.T, tr *Transport, id string) {
	t.Helper()
	tr.dispatchNotification([]byte(fmt.Sprintf(
		`{"method":"eth_subscription","params":{"subscription":%q,"result":"0x1"}}`, id)))
}

// TestPendingNotificationsBoundedAndCleanedUp pins the two bounds and cleanup
// exits on the transport-level pending buffer. At the global cap the oldest
// never-bound ID is retired so a new subscription's first notification is
// retained instead of being silently discarded.
func TestPendingNotificationsBoundedAndCleanedUp(t *testing.T) {
	tr := newPendingTestTransport(t)

	for i := 0; i < maxPendingSubscriptionIDs+50; i++ {
		pushNotification(t, tr, fmt.Sprintf("0x%064x", i))
	}
	if got := len(tr.pendingNotifications); got != maxPendingSubscriptionIDs {
		t.Fatalf("pending IDs = %d, want bounded size %d", got, maxPendingSubscriptionIDs)
	}
	newest := fmt.Sprintf("0x%064x", maxPendingSubscriptionIDs+49)
	if got := len(tr.pendingNotifications[newest]); got != 1 {
		t.Fatalf("newest pending ID retained %d notifications, want 1", got)
	}
	oldest := fmt.Sprintf("0x%064x", 0)
	if _, ok := tr.pendingNotifications[oldest]; ok {
		t.Fatal("oldest never-bound pending ID was not retired at the global cap")
	}
	// An ID that already holds a buffer keeps filling it up to its own cap.
	survivor := newest
	for i := 0; i < maxPendingNotificationsPerID+10; i++ {
		pushNotification(t, tr, survivor)
	}
	if got := len(tr.pendingNotifications[survivor]); got != maxPendingNotificationsPerID {
		t.Fatalf("per-ID buffer = %d, want the %d cap", got, maxPendingNotificationsPerID)
	}

	// Binding an ID flushes and removes its buffer; delivering to the bound
	// stream must not recreate one.
	stream := &NotificationStream{
		owner:  tr,
		events: make(chan Notification, maxPendingNotificationsPerID+8),
		errs:   make(chan error, 1),
	}
	tr.streams[stream] = struct{}{}
	const boundID = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1"
	if !stream.bindSubscriptionID(boundID) {
		t.Fatal("bindSubscriptionID failed")
	}
	pushNotification(t, tr, boundID)
	if _, ok := tr.pendingNotifications[boundID]; ok {
		t.Fatal("bound ID left a pending buffer behind")
	}

	// Unsubscribing removes a lingering pending entry for the same ID —
	// a server that keeps pushing after the unsubscribe must not pin
	// the buffer forever.
	tr.pendingNotifications[boundID] = append(tr.pendingNotifications[boundID],
		Notification{SubscriptionID: boundID})
	tr.removeNotificationStream(stream)
	if _, ok := tr.pendingNotifications[boundID]; ok {
		t.Fatal("unsubscribed ID left a pending buffer behind")
	}

	// abort kills the socket and with it every pending buffer.
	pushNotification(t, tr, "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	tr.abort(errors.New("socket dead"))
	if got := len(tr.pendingNotifications); got != 0 {
		t.Fatalf("abort left pending notifications behind: %d", got)
	}
}

func TestBindingRemovesPendingIDFromEvictionOrder(t *testing.T) {
	tr := newPendingTestTransport(t)
	const id = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pushNotification(t, tr, id)

	stream := &NotificationStream{
		owner:  tr,
		events: make(chan Notification, 2),
		errs:   make(chan error, 1),
	}
	tr.streams[stream] = struct{}{}
	if !stream.bindSubscriptionID(id) {
		t.Fatal("bindSubscriptionID failed")
	}
	if _, ok := tr.pendingNotifications[id]; ok {
		t.Fatal("binding left the pending buffer behind")
	}
	for _, pendingID := range tr.pendingNotificationOrder {
		if pendingID == id {
			t.Fatal("binding left the ID in pending eviction order")
		}
	}
}

// TestPendingPlaceholderStreamDoesNotMisrouteOtherSubscriptions pins the
// Subscribe-path registration contract: a stream that is still awaiting its
// eth_subscribe answer carries the pending placeholder from birth, so a
// notification for another subscription on the same shared connection must
// reach only that subscription's stream — never the pending one, and never
// through the generic "" routing the direct-open path documents.
func TestPendingPlaceholderStreamDoesNotMisrouteOtherSubscriptions(t *testing.T) {
	tr := newPendingTestTransport(t)

	bound := &NotificationStream{
		owner:          tr,
		events:         make(chan Notification, 8),
		errs:           make(chan error, 1),
		subscriptionID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	tr.streams[bound] = struct{}{}
	tr.subscriptionIDs[bound.subscriptionID] = bound

	pendingStream, err := tr.openNotificationStreamWithID(context.Background(), "source-b", 1, 8, pendingSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if pendingStream.subscriptionID != pendingSubscriptionID {
		t.Fatal("the subscribe-path stream must register with the pending placeholder")
	}

	// A notification for the OTHER subscription must not land in the
	// pending stream, and an unknown ID must buffer instead of delivering.
	pushNotification(t, tr, bound.subscriptionID)
	pushNotification(t, tr, "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	select {
	case event := <-pendingStream.Events():
		t.Fatalf("pending stream misrouted a foreign notification: %+v", event)
	default:
	}
	select {
	case event := <-bound.Events():
		if event.SubscriptionID != bound.subscriptionID {
			t.Fatalf("bound stream received the wrong notification: %+v", event)
		}
	default:
		t.Fatal("bound stream lost its own notification")
	}
}

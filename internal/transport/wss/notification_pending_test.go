package wss

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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
	assertPendingNotificationOrder(t, tr)
	newest := fmt.Sprintf("0x%064x", maxPendingSubscriptionIDs+49)
	// An ID that already holds a buffer keeps filling it up to its own cap.
	survivor := newest
	for i := 0; i < maxPendingNotificationsPerID+10; i++ {
		pushNotification(t, tr, survivor)
	}
	if got := len(tr.pendingNotifications[survivor]); got != maxPendingNotificationsPerID {
		t.Fatalf("per-ID buffer = %d, want the %d cap", got, maxPendingNotificationsPerID)
	}
	assertPendingNotificationOrder(t, tr)

	// Binding an ID flushes and removes its buffer; delivering to the bound
	// stream must not recreate one.
	stream := &NotificationStream{
		owner:  tr,
		events: make(chan Notification, maxPendingNotificationsPerID+8),
		errs:   make(chan error, 1),
		done:   make(chan struct{}),
	}
	tr.streams[stream] = struct{}{}
	boundID := survivor
	if !stream.bindSubscriptionID(boundID) {
		t.Fatal("bindSubscriptionID failed")
	}
	pushNotification(t, tr, boundID)
	if _, ok := tr.pendingNotifications[boundID]; ok {
		t.Fatal("bound ID left a pending buffer behind")
	}
	assertPendingNotificationOrder(t, tr)

	// Unsubscribing removes a lingering pending entry for the same ID —
	// a server that keeps pushing after the unsubscribe must not pin
	// the buffer forever.
	tr.pendingNotifications[boundID] = append(tr.pendingNotifications[boundID],
		Notification{SubscriptionID: boundID})
	tr.pendingNotificationOrder = append(tr.pendingNotificationOrder, boundID)
	stream.Unsubscribe()
	if _, ok := tr.pendingNotifications[boundID]; ok {
		t.Fatal("unsubscribed ID left a pending buffer behind")
	}
	assertPendingNotificationOrder(t, tr)

	// abort kills the socket and with it every pending buffer.
	pushNotification(t, tr, "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	tr.abort(errors.New("socket dead"))
	if got := len(tr.pendingNotifications); got != 0 {
		t.Fatalf("abort left pending notifications behind: %d", got)
	}
	assertPendingNotificationOrder(t, tr)
}

func TestCloseClearsPendingNotifications(t *testing.T) {
	tr := newPendingTestTransport(t)
	pushNotification(t, tr, "never-bound")
	tr.Close()
	if got := len(tr.pendingNotifications); got != 0 {
		t.Fatalf("Close left pending notifications behind: %d", got)
	}
	assertPendingNotificationOrder(t, tr)
}

func assertPendingNotificationOrder(t *testing.T, tr *Transport) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if got, want := len(tr.pendingNotificationOrder), len(tr.pendingNotifications); got != want {
		t.Fatalf("pending eviction order has %d IDs, want %d", got, want)
	}
	seen := make(map[string]bool, len(tr.pendingNotificationOrder))
	for _, id := range tr.pendingNotificationOrder {
		if seen[id] {
			t.Fatalf("pending eviction order repeats ID %q", id)
		}
		seen[id] = true
		if _, ok := tr.pendingNotifications[id]; !ok {
			t.Fatalf("pending eviction order retains removed ID %q", id)
		}
	}
}

func TestSubscribeAtPendingIDCapDeliversEarlyNotification(t *testing.T) {
	const subscriptionID = "new-subscription"
	const result = `{"number":"0x1234"}`
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		// Every foreign ID fills a pending slot while Subscribe is awaiting
		// its response. Wire ordering makes the cap and early-event race
		// deterministic without sleeps or inspection of the pending map.
		for i := range maxPendingSubscriptionIDs {
			peer.writeFrame(notificationFrame(fmt.Sprintf("foreign-%d", i), `{"number":"0xffff"}`))
		}
		peer.writeFrame(notificationFrame(subscriptionID, result))
		peer.respond(request, `"`+subscriptionID+`"`)
		peer.respond(<-peer.requests, `true`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	subscription, err := tr.Subscribe(ctx, "newHeads")
	if err != nil {
		t.Fatalf("Subscribe at pending-ID cap: %v", err)
	}
	defer subscription.Unsubscribe()
	select {
	case event, ok := <-subscription.Events():
		if !ok || event.Method != "eth_subscription" || event.SubscriptionID != subscriptionID || string(event.Result) != result {
			t.Fatalf("early subscription event = %+v, open = %v", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("subscription lost the early notification at the pending-ID cap")
	}
	// All pre-response notifications have been dispatched before Subscribe
	// returns, so an empty queue here proves foreign-ID isolation.
	select {
	case event := <-subscription.Events():
		t.Fatalf("subscription received an extra or foreign event: %+v", event)
	default:
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

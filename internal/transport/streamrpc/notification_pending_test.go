package streamrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
)

func newPendingTestTransport(t *testing.T) *Transport {
	t.Helper()
	conn := newScriptedPeer(t, func(peer *scriptedPeer) {
		for range peer.requests {
		}
	})
	tr := &Transport{conn: conn, ids: rpcwire.NewIDGen(), pending: make(map[string]chan frameResult), streams: make(map[*NotificationStream]struct{}), subscriptionIDs: make(map[string]*NotificationStream), pendingNotifications: make(map[string][]Notification)}
	t.Cleanup(tr.Close)
	return tr
}

func pushNotification(t *testing.T, tr *Transport, id string) {
	t.Helper()
	tr.dispatchNotification([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":"0x1"}}`, id)))
}

func TestPendingNotificationsBoundedAndCleanedUp(t *testing.T) {
	tr := newPendingTestTransport(t)
	for i := 0; i < 1000; i++ {
		pushNotification(t, tr, fmt.Sprint(i))
	}
	if len(tr.pendingNotifications) != 0 {
		t.Fatal("unknown IDs retained without an establishing subscription")
	}
	stream, err := tr.openNotificationStreamWithID(context.Background(), "a", 1, 8, pendingSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Unsubscribe()
	for i := 0; i < defaultPendingMessages; i++ {
		pushNotification(t, tr, fmt.Sprint(i))
	}
	if tr.pendingMessages != defaultPendingMessages || tr.pendingBytes == 0 {
		t.Fatal("pending accounting missing")
	}
	pushNotification(t, tr, "overflow")
	if stream.bindSubscriptionID("0") {
		t.Fatal("setup silently survived dropped notification")
	}
	tr.mu.Lock()
	pendingMessages, pendingBytes := tr.pendingMessages, tr.pendingBytes
	tr.mu.Unlock()
	if pendingMessages != 0 || pendingBytes != 0 {
		t.Fatal("failed setup retained bytes")
	}
	if err := <-stream.Err(); !errors.Is(err, ErrNotificationBackpressure) {
		t.Fatalf("setup error %v", err)
	}
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

func TestCloseDoesNotRebuildPendingNotifications(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	requestsReady := make(chan struct{})
	afterClose := make(chan struct{})
	releaseLast := make(chan struct{})
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		first := <-peer.requests
		last := <-peer.requests
		close(requestsReady)
		select {
		case <-afterClose:
		case <-ctx.Done():
			return
		}
		peer.writeFrame(notificationFrame("never-bound", `"0x1"`))
		peer.respond(first, `"0x2"`)
		select {
		case <-releaseLast:
		case <-ctx.Done():
			return
		}
		peer.respond(last, `"0x2"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	type callResult struct {
		value string
		err   error
	}
	results := make(chan callResult, 2)
	for range 2 {
		go func() {
			var value string
			err := tr.Call(ctx, "eth_blockNumber").Decode(&value)
			results <- callResult{value, err}
		}()
	}
	select {
	case <-requestsReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pushNotification(t, tr, "before-close")
	tr.Close()
	close(afterClose)
	select {
	case result := <-results:
		if result.err != nil || result.value != "0x2" {
			t.Fatalf("in-flight response = %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The first response is a reader barrier for the notification. Keeping
	// another call pending prevents socket teardown from hiding a rebuilt map.
	tr.mu.Lock()
	buffered, ordered := len(tr.pendingNotifications), len(tr.pendingNotificationOrder)
	tr.mu.Unlock()
	close(releaseLast)
	select {
	case result := <-results:
		if result.err != nil || result.value != "0x2" {
			t.Fatalf("last in-flight response = %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if buffered != 0 || ordered != 0 {
		t.Fatalf("Close rebuilt pending notifications: map=%d order=%d", buffered, ordered)
	}
}

func TestClosedTransportIgnoresNotification(t *testing.T) {
	tr := newPendingTestTransport(t)
	tr.Close()
	pushNotification(t, tr, "after-close")
	if len(tr.pendingNotifications) != 0 || len(tr.pendingNotificationOrder) != 0 {
		t.Fatal("closed transport rebuilt pending notifications")
	}
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

func TestSubscribePendingBudgetOverflowFailsSetup(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		for i := 0; i <= defaultPendingMessages; i++ {
			peer.writeFrame(notificationFrame(fmt.Sprintf("foreign-%d", i), `"0x1"`))
		}
		peer.respond(request, `"new-subscription"`)
		unsubscribe := <-peer.requests
		var wire struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(unsubscribe, &wire); err != nil {
			peer.t.Errorf("decode unsubscribe: %v", err)
			return
		}
		if wire.Method != "eth_unsubscribe" || len(wire.Params) != 1 || string(wire.Params[0]) != `"new-subscription"` {
			peer.t.Errorf("setup failure did not retire server subscription: %s", unsubscribe)
			return
		}
		peer.respond(unsubscribe, `true`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := tr.Subscribe(ctx, "newHeads"); !errors.Is(err, ErrNotificationBackpressure) {
		t.Fatalf("setup overflow %v", err)
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

	bound, err := tr.openNotificationStreamWithID(context.Background(), "a", 1, 8, "")
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Unsubscribe()
	if !bound.bindSubscriptionID("bound") {
		t.Fatal("bind")
	}

	pendingStream, err := tr.openNotificationStreamWithID(context.Background(), "source-b", 1, 8, pendingSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	defer pendingStream.Unsubscribe()
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
	case <-time.After(time.Second):
		t.Fatal("bound stream lost its own notification")
	}
}

package wss

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
)

type notificationBudget struct {
	mu   sync.Mutex
	used uint64
}

func (b *notificationBudget) reserve(n uint64) (func(), error) {
	b.mu.Lock()
	b.used += n
	b.mu.Unlock()
	return sync.OnceFunc(func() { b.mu.Lock(); b.used -= n; b.mu.Unlock() }), nil
}
func (b *notificationBudget) bytes() uint64 { b.mu.Lock(); defer b.mu.Unlock(); return b.used }

func TestNotificationReservationTransfersUntilReceiverRelease(t *testing.T) {
	tr := newPendingTestTransport(t)
	b := &notificationBudget{}
	s, err := tr.openNotificationStreamPolicy(context.Background(), "a", 1, transport.NotificationPolicy{Messages: 2, Bytes: 4096, MaxMessageBytes: 1024, Reserve: b.reserve}, "")
	if err != nil {
		t.Fatal(err)
	}
	pushNotification(t, tr, "one")
	var n Notification
	select {
	case n = <-s.Events():
	case <-time.After(time.Second):
		t.Fatal("missing delivery")
	}
	if b.bytes() == 0 {
		t.Fatal("send released receiver-owned bytes")
	}
	s.Unsubscribe()
	if b.bytes() == 0 {
		t.Fatal("unsubscribe released already handed off bytes")
	}
	n.Release()
	if b.bytes() != 0 {
		t.Fatal("receiver release leaked bytes")
	}
}

func TestUnreadProducerFailureIsReleasedByOwnerClose(t *testing.T) {
	tr := newPendingTestTransport(t)
	b := &notificationBudget{}
	s, err := tr.openNotificationStreamPolicy(context.Background(), "a", 1, transport.NotificationPolicy{Messages: 1, Bytes: 4096, MaxMessageBytes: 1024, Reserve: b.reserve}, "")
	if err != nil {
		t.Fatal(err)
	}
	pushNotification(t, tr, "one")
	pushNotification(t, tr, "two")
	if b.bytes() == 0 {
		t.Fatal("no queued reservation")
	}
	tr.Close()
	if b.bytes() != 0 {
		t.Fatalf("owner close retained %d raw bytes", b.bytes())
	}
	if _, ok := <-s.Events(); ok {
		t.Fatal("owner close retained notification")
	}
	tr.mu.Lock()
	streams := len(tr.streams)
	tr.mu.Unlock()
	if streams != 0 {
		t.Fatal("closed streams remained registered")
	}
}

func TestByteLimitAndPendingTransferDoNotDropSilently(t *testing.T) {
	tr := newPendingTestTransport(t)
	tr.pendingBytesLimit = 1024
	tr.pendingMessagesLimit = 20
	s, err := tr.openNotificationStreamWithID(context.Background(), "a", 1, 8, pendingSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Unsubscribe()
	for i := 0; i < 4; i++ {
		pushNotification(t, tr, "early")
	}
	if s.bindSubscriptionID("early") {
		t.Fatal("pre-bind byte overflow survived")
	}
	if err := <-s.Err(); !errors.Is(err, ErrNotificationBackpressure) {
		t.Fatalf("terminal %v", err)
	}
	tr.mu.Lock()
	pendingBytes := tr.pendingBytes
	tr.mu.Unlock()
	if pendingBytes != 0 {
		t.Fatal("pending bytes survived failed setup")
	}
}

func TestProducerFailureThenContextCancelDiscardsUnreadNotifications(t *testing.T) {
	tr := newPendingTestTransport(t)
	b := &notificationBudget{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := tr.openNotificationStreamPolicy(ctx, "a", 1, transport.NotificationPolicy{Messages: 1, Bytes: 4096, MaxMessageBytes: 1024, Reserve: b.reserve}, "")
	if err != nil {
		t.Fatal(err)
	}
	pushNotification(t, tr, "one")
	s.finish(errors.New("producer failed"))
	<-s.done
	cancel()
	select {
	case <-s.pumpDone:
	case <-time.After(time.Second):
		t.Fatal("context cancellation stopped watching the unread producer failure")
	}
	if b.bytes() != 0 {
		t.Fatalf("context cancellation retained %d bytes", b.bytes())
	}
}

func TestNotificationOversizeAndReservationFailuresKeepDistinctCauses(t *testing.T) {
	for _, which := range []string{"message", "reservation"} {
		t.Run(which, func(t *testing.T) {
			tr := newPendingTestTransport(t)
			wanted := errors.New("shared budget exhausted")
			policy := transport.NotificationPolicy{Messages: 1, Bytes: 4096, MaxMessageBytes: 1024}
			if which == "message" {
				policy.MaxMessageBytes = 16
				wanted = transport.ErrBodyTooLarge
			} else {
				policy.Reserve = func(uint64) (func(), error) { return nil, wanted }
			}
			s, err := tr.openNotificationStreamPolicy(context.Background(), "a", 1, policy, "")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Unsubscribe()
			pushNotification(t, tr, "one")
			select {
			case err := <-s.Err():
				if !errors.Is(err, wanted) {
					t.Fatalf("cause was overwritten: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("admission failure did not terminate the stream")
			}
		})
	}
}

func TestConcurrentNotificationAbortPreservesHandoffAccounting(t *testing.T) {
	for range 200 {
		tr := newPendingTestTransport(t)
		b := &notificationBudget{}
		s, err := tr.openNotificationStreamPolicy(context.Background(), "a", 1, transport.NotificationPolicy{Messages: 1, Bytes: 4096, MaxMessageBytes: 1024, Reserve: b.reserve}, "")
		if err != nil {
			t.Fatal(err)
		}
		pushNotification(t, tr, "one")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for n := range s.Events() {
				if b.bytes() == 0 {
					t.Error("handoff reservation released before decode")
				}
				n.Release()
			}
		}()
		go func() { defer wg.Done(); tr.Close() }()
		wg.Wait()
		if b.bytes() != 0 {
			t.Fatal("abort leaked reservation")
		}
	}
}

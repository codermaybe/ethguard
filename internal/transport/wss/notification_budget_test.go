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

// Keep the failed setup registered while its pump waits to publish the
// terminal error. This reproduces the interval before asynchronous removal
// without making receipt of Err a cleanup barrier.
func holdFailedSetup(t *testing.T, tr *Transport) (*NotificationStream, func()) {
	t.Helper()
	tr.pendingMessagesLimit = 1
	s, err := tr.openNotificationStreamWithID(context.Background(), "failed", 1, 8, pendingSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	held := errors.New("hold terminal delivery")
	s.errs <- held
	release := sync.OnceFunc(func() {
		if err := <-s.errs; !errors.Is(err, held) {
			t.Errorf("terminal delivery barrier replaced: %v", err)
		}
	})
	t.Cleanup(func() { release(); s.Unsubscribe() })
	pushNotification(t, tr, "early")
	pushNotification(t, tr, "overflow")
	if s.bindSubscriptionID("early") {
		t.Fatal("overflowed setup bound successfully")
	}
	return s, release
}

func TestFailedSetupDoesNotRebuildPendingNotifications(t *testing.T) {
	tr := newPendingTestTransport(t)
	holdFailedSetup(t, tr)
	pushNotification(t, tr, "after-failure")
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.pendingBytes != 0 || tr.pendingMessages != 0 || len(tr.pendingNotifications) != 0 || len(tr.pendingNotificationOrder) != 0 {
		t.Fatalf("failed setup rebuilt pending notifications: bytes=%d messages=%d ids=%d", tr.pendingBytes, tr.pendingMessages, len(tr.pendingNotifications))
	}
}

func TestFailedAndLiveSetupsKeepPendingOwnership(t *testing.T) {
	for _, retireFailed := range []bool{false, true} {
		name := "bind-before-failed-pump-exits"
		if retireFailed {
			name = "failed-pump-exits-before-bind"
		}
		t.Run(name, func(t *testing.T) {
			tr := newPendingTestTransport(t)
			failed, release := holdFailedSetup(t, tr)
			tr.pendingMessagesLimit = 20
			live, err := tr.openNotificationStreamWithID(context.Background(), "live", 2, 8, pendingSubscriptionID)
			if err != nil {
				t.Fatal(err)
			}
			defer live.Unsubscribe()
			pushNotification(t, tr, "late-failed")
			pushNotification(t, tr, "live-id")
			if retireFailed {
				release()
				<-failed.pumpDone
			}
			tr.mu.Lock()
			messages, retained := tr.pendingMessages, len(tr.pendingNotifications["live-id"])
			tr.mu.Unlock()
			if messages != 2 || retained != 1 {
				t.Fatalf("live setup lost its pending notification: messages=%d live=%d", messages, retained)
			}
			if !live.bindSubscriptionID("live-id") {
				t.Fatal("live setup failed to bind")
			}
			select {
			case n := <-live.Events():
				if n.SubscriptionID != "live-id" || n.SourceID != "live" || n.Generation != 2 {
					t.Fatalf("live setup received another setup's notification: %+v", n)
				}
				n.Release()
			case <-time.After(time.Second):
				t.Fatal("live setup lost its early notification")
			}
			tr.mu.Lock()
			defer tr.mu.Unlock()
			if tr.pendingBytes != 0 || tr.pendingMessages != 0 || len(tr.pendingNotifications) != 0 || len(tr.pendingNotificationOrder) != 0 {
				t.Fatal("last live setup bound but failed setup retained pending notifications")
			}
		})
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

package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
	internalsubscription "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/internal/transport"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func receiptSubscriptionField(t *testing.T, raw json.RawMessage, field string, value json.RawMessage) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields[field] = value
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func receiptSubscriptionBatch(t *testing.T, rows ...json.RawMessage) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReceiptNotificationRejectsDuplicateBatchIdentity(t *testing.T) {
	first := prerequisiteReceipt(t, common.HexToHash("0x11"), 1)
	for _, tc := range []struct {
		name string
		next json.RawMessage
	}{
		{"same transaction", prerequisiteReceipt(t, common.HexToHash("0x11"), 4)},
		{"same index", prerequisiteReceipt(t, common.HexToHash("0x33"), 1)},
		{"same row", first},
		{"same hash different height", receiptSubscriptionField(t, first, "blockNumber", json.RawMessage(`"0x8"`))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := decodeReceiptNotification(receiptSubscriptionBatch(t, first, tc.next), "a"); err == nil {
				t.Fatal("inconsistent receipt identities were admitted")
			}
		})
	}
	second := prerequisiteReceipt(t, common.HexToHash("0x33"), 4)
	left, values, err := decodeReceiptNotification(receiptSubscriptionBatch(t, first, second), "a")
	if err != nil || len(values) != 2 {
		t.Fatalf("valid noncontiguous subset rejected: %v", err)
	}
	right, _, err := decodeReceiptNotification(receiptSubscriptionBatch(t, second, first), "b")
	if err != nil || left.ContextKey != right.ContextKey || left.Key != right.Key {
		t.Fatalf("subset order changed candidate identity: %v", err)
	}
}

func TestReceiptPullChecksWholeBatchBeforeFiltering(t *testing.T) {
	wanted := common.HexToHash("0x11")
	first := prerequisiteReceipt(t, wanted, 1)
	other := prerequisiteReceipt(t, common.HexToHash("0x33"), 4)
	seed, _, err := decodeReceiptNotification(receiptSubscriptionBatch(t, first), "a")
	if err != nil {
		t.Fatal(err)
	}
	seed.Generation = 1
	hashes := []ethapi.Hash{ethapi.Hash(wanted.Hex())}
	query := &ethapi.TransactionReceiptsQuery{TransactionHashes: &hashes}
	for _, tc := range []struct {
		name string
		rows []json.RawMessage
	}{
		{"filtered wrong height", []json.RawMessage{first, receiptSubscriptionField(t, other, "blockNumber", json.RawMessage(`"0x8"`))}},
		{"filtered duplicate hash", []json.RawMessage{first, other, prerequisiteReceipt(t, common.HexToHash("0x33"), 5)}},
		{"filtered duplicate index", []json.RawMessage{first, prerequisiteReceipt(t, common.HexToHash("0x33"), 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &pullTestSource{id: "b", pullRaw: receiptSubscriptionBatch(t, tc.rows...)}
			if _, err := pullReceiptNotification(context.Background(), source, internalsubscription.KindTransactionReceipts, seed.ContextKey, seed, query); err == nil {
				t.Fatal("Pull hid invalid whole-batch identity behind its filter")
			}
		})
	}
}

func TestAggregatedSubscriptionRetiresInnerBeforeReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := &blockingCleanupSubscription{
			aggregateTestSubscription: aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error, 1)},
			started:                   make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
		}
		release := sync.OnceFunc(func() { close(first.release) })
		replacement := &trackedSubscription{events: make(chan transport.Notification), errs: make(chan error), unsubscribed: make(chan struct{})}
		a := &reconnectTestSource{id: "a", subs: []transport.Subscription{first, replacement}}
		b := &aggregateTestSource{id: "b", sub: &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)}}
		sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{a, b}, internalsubscription.KindLogs, nil, make(chan string), decodeTestEvent)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { release(); sub.Unsubscribe() }()
		first.errs <- errors.New("local stream failed")
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		select {
		case <-first.started:
		default:
			t.Fatal("failed inner was not unsubscribed before replacement")
		}
		if _, err := sub.(*aggregateEthSubscription[string]).sessions.Current("a"); !errors.Is(err, internalsubscription.ErrSessionStale) {
			t.Fatalf("failed session stayed active during cleanup: %v", err)
		}
		if got := a.subscriptions(); got != 1 {
			t.Fatalf("replacement opened before retired inner finished: %d subscriptions", got)
		}
		release()
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if got := a.subscriptions(); got != 2 {
			t.Fatalf("replacement did not open after cleanup: %d subscriptions", got)
		}
		sub.Unsubscribe()
		select {
		case <-replacement.unsubscribed:
		default:
			t.Fatal("replacement cleanup was not joined")
		}
	})
}

func TestSubscriptionReleasesReceivedNotifications(t *testing.T) {
	for _, multi := range []bool{false, true} {
		for _, bad := range []bool{false, true} {
			t.Run(fmt.Sprintf("multi=%v/bad=%v", multi, bad), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					stream := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
					var released atomic.Int32
					decode := func(raw []byte) (string, error) {
						if bad {
							return "", errors.New("bad receipt")
						}
						return string(raw), nil
					}
					out := make(chan string, 1)
					var sub ethereum.Subscription
					if multi {
						other := &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)}
						var err error
						sub, err = startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{&aggregateTestSource{id: "a", sub: stream}, &aggregateTestSource{id: "b", sub: other}}, internalsubscription.KindLogs, nil, out, func(raw []byte, sourceID string) (internalsubscription.Observation, string, error) {
							value, err := decode(raw)
							return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: "event", Key: value, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, value, err
						})
						if err != nil {
							t.Fatal(err)
						}
					} else {
						sub = startSubscription(context.Background(), stream, out, decode)
					}
					defer sub.Unsubscribe()
					stream.events <- transport.Notification{Result: json.RawMessage(`"receipt"`), Release: func() { released.Add(1) }}
					synctest.Wait()
					if got := released.Load(); got != 1 {
						t.Fatalf("notification released %d times, want 1", got)
					}
				})
			})
		}
	}
}

func TestAggregatedSubscriptionReleasesRejectedSessionNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := &aggregateTestSubscription{events: make(chan transport.Notification, 2), errs: make(chan error)}
		other := &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)}
		var released atomic.Int32
		sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{&aggregateTestSource{id: "a", sub: stream}, &aggregateTestSource{id: "b", sub: other}}, internalsubscription.KindLogs, nil, make(chan string), func([]byte, string) (internalsubscription.Observation, string, error) {
			t.Error("notification with foreign session reached decoder")
			return internalsubscription.Observation{}, "", errors.New("unreachable")
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Unsubscribe()
		stream.events <- transport.Notification{SourceID: "foreign", Release: func() { released.Add(1) }}
		stream.events <- transport.Notification{Generation: 99, Release: func() { released.Add(1) }}
		synctest.Wait()
		if got := released.Load(); got != 2 {
			t.Fatalf("rejected notifications released %d times, want 2", got)
		}
	})
}

func TestSingleSubscriptionTerminalJoinsInnerCleanup(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", failure), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				inner := &blockingCleanupSubscription{
					aggregateTestSubscription: aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error, 1)},
					started:                   make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
				}
				release := sync.OnceFunc(func() { close(inner.release) })
				sub := startSubscription(context.Background(), inner, make(chan string), func(raw []byte) (string, error) { return string(raw), nil })
				defer func() { release(); sub.Unsubscribe() }()
				if failure {
					inner.errs <- errors.New("stream failed")
				} else {
					close(inner.events)
					close(inner.errs)
				}
				synctest.Wait()
				select {
				case <-inner.started:
				default:
					t.Fatal("terminal subscription did not clean up its inner")
				}
				select {
				case <-sub.(*ethSubscription).done:
					t.Fatal("terminal subscription completed before inner cleanup")
				default:
				}
				release()
				synctest.Wait()
				select {
				case <-sub.(*ethSubscription).done:
				default:
					t.Fatal("terminal subscription did not complete after cleanup")
				}
				if err := <-sub.Err(); (err != nil) != failure {
					t.Fatalf("terminal error = %v, failure=%v", err, failure)
				}
			})
		})
	}
}

func TestAggregatedSubscriptionSlowConsumerKeepsDeadline(t *testing.T) {
	for _, withPull := range []bool{false, true} {
		t.Run(fmt.Sprintf("pull=%v", withPull), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := &aggregateTestSubscription{events: make(chan transport.Notification, 2), errs: make(chan error)}
				b := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
				var pull subscriptionPullFactory[string]
				wait := 31 * time.Second
				if withPull {
					wait = 3 * time.Second
					pull = func(context.Context, identifiedSubscriptionSource, internalsubscription.Kind, string, internalsubscription.Observation) (internalsubscription.Observation, error) {
						return internalsubscription.Observation{}, errors.New("receipt unavailable")
					}
				}
				sub, err := startAggregatedSubscriptionWithPull(context.Background(), []identifiedSubscriptionSource{&aggregateTestSource{id: "a", sub: a}, &aggregateTestSource{id: "b", sub: b}}, internalsubscription.KindLogs, nil, make(chan string), decodeTestEvent, pull)
				if err != nil {
					t.Fatal(err)
				}
				defer sub.Unsubscribe()
				a.events <- transport.Notification{Result: json.RawMessage(`"ready"`)}
				b.events <- transport.Notification{Result: json.RawMessage(`"ready"`)}
				synctest.Wait()
				a.events <- transport.Notification{Result: json.RawMessage(`"missing"`)}
				synctest.Wait()
				time.Sleep(wait)
				synctest.Wait()
				select {
				case err := <-sub.Err():
					if !errors.Is(err, internalsubscription.ErrQuorumTimeout) {
						t.Fatalf("terminal error = %v", err)
					}
				default:
					t.Fatal("unread output froze the missing event's deadline")
				}
			})
		})
	}
}

func TestCompletedSubscriptionReleasesOwnedReferences(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
		b := &aggregateTestSubscription{events: make(chan transport.Notification), errs: make(chan error)}
		sub, err := startAggregatedSubscriptionWithPull(context.Background(), []identifiedSubscriptionSource{&aggregateTestSource{id: "a", sub: a}, &aggregateTestSource{id: "b", sub: b}}, internalsubscription.KindLogs, nil, make(chan string), decodeTestEvent, func(ctx context.Context, _ identifiedSubscriptionSource, _ internalsubscription.Kind, _ string, _ internalsubscription.Observation) (internalsubscription.Observation, error) {
			<-ctx.Done()
			return internalsubscription.Observation{}, ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		a.events <- transport.Notification{Result: json.RawMessage(`"retained receipt"`)}
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		sub.Unsubscribe()
		owner := sub.(*aggregateEthSubscription[string])
		if owner.inner != nil || owner.pending != nil || owner.probes != nil || owner.agg != nil || owner.out != nil || owner.decode != nil || owner.pullFactory != nil {
			t.Fatal("completed handle retains its historical payload owners")
		}
		sub.Unsubscribe()
		if err, ok := <-sub.Err(); ok || err != nil {
			t.Fatalf("repeat close changed terminal state: %v %v", err, ok)
		}
	})
}

func TestAggregatedSubscriptionPendingOutputKeepsFIFO(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
		b := &aggregateTestSubscription{events: make(chan transport.Notification, 1), errs: make(chan error)}
		out := make(chan string)
		sub, err := startAggregatedSubscription(context.Background(), []identifiedSubscriptionSource{&aggregateTestSource{id: "a", sub: a}, &aggregateTestSource{id: "b", sub: b}}, internalsubscription.KindLogs, nil, out, decodeTestEvent)
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Unsubscribe()
		for _, value := range []string{`"first"`, `"second"`, `"third"`} {
			a.events <- transport.Notification{Result: json.RawMessage(value)}
			b.events <- transport.Notification{Result: json.RawMessage(value)}
			synctest.Wait()
		}
		for _, want := range []string{`"first"`, `"second"`, `"third"`} {
			if got := <-out; got != want {
				t.Fatalf("delivery = %s, want %s", got, want)
			}
		}
	})
}

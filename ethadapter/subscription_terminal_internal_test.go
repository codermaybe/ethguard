package ethadapter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
)

// This source makes producer termination independent of Next and delays Close
// so the test can distinguish notification from the cleanup completion barrier.
type independentBridgeNative struct {
	events       chan int
	nextEntered  chan struct{}
	terminated   chan struct{}
	closeEntered chan struct{}
	closeGate    chan struct{}
	closed       chan struct{}
	closeOnce    sync.Once
	gateOnce     sync.Once
	cause        error
	leases       atomic.Int64
}

func newIndependentBridgeNative() *independentBridgeNative {
	return &independentBridgeNative{events: make(chan int, 8), nextEntered: make(chan struct{}, 16), terminated: make(chan struct{}), closeEntered: make(chan struct{}), closeGate: make(chan struct{}), closed: make(chan struct{})}
}
func (n *independentBridgeNative) Basis() ethguard.HeadEvidenceBasis {
	return ethguard.HeadEvidenceBasis{DomainID: "independent", Members: []string{"source"}, Quorum: 1}
}
func (n *independentBridgeNative) NextWithBudget(ctx context.Context, budget ethguard.SubscriptionMemoryBudget) (int, func(), error) {
	n.nextEntered <- struct{}{}
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case value := <-n.events:
		release, err := budget.Reserve(1024)
		if err != nil {
			return 0, nil, err
		}
		n.leases.Add(1)
		return value, sync.OnceFunc(func() { n.leases.Add(-1); release() }), nil
	}
}
func (n *independentBridgeNative) Terminated() <-chan struct{} { return n.terminated }
func (n *independentBridgeNative) TerminalError() error        { return n.cause }
func (n *independentBridgeNative) Close() error {
	n.closeOnce.Do(func() { close(n.closeEntered); <-n.closeGate; close(n.closed) })
	return nil
}
func (n *independentBridgeNative) unblockClose() { n.gateOnce.Do(func() { close(n.closeGate) }) }

func independentBridgeWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("independent bridge synchronization timed out")
	}
}
func independentBridgeEventually(t *testing.T, predicate func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for !predicate() {
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("independent bridge condition timed out")
		}
	}
}

func independentStartBridge(t *testing.T, native *independentBridgeNative, ch chan<- int) (*Client, *channelSubscription[int]) {
	t.Helper()
	core, err := ethguard.Dial("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	c := New(core)
	t.Cleanup(func() { native.unblockClose(); c.Close() })
	sub, err := startChannel(c, t.Context(), "independent", ch, nil,
		func(context.Context) (nativeSubscription[int], error) { return native, nil },
		func(value int) ([]int, ConversionReport, error) { return []int{value}, ConversionReport{}, nil },
		func(int) *SubscriptionAudit { return &SubscriptionAudit{} })
	if err != nil {
		t.Fatal(err)
	}
	return c, sub.(*channelSubscription[int])
}

func independentBridgeAssertSettled(t *testing.T, native *independentBridgeNative, sub *channelSubscription[int], want error) {
	t.Helper()
	independentBridgeWait(t, sub.Done())
	independentBridgeWait(t, native.closed)
	if got := <-sub.Err(); !errors.Is(got, want) {
		t.Fatalf("terminal=%v want %v", got, want)
	}
	if _, ok := <-sub.Err(); ok {
		t.Fatal("terminal channel not closed")
	}
	sub.memory.mu.Lock()
	used := sub.memory.used
	sub.memory.mu.Unlock()
	if used != 0 || native.leases.Load() != 0 {
		t.Fatalf("settled ownership remains: bytes=%d leases=%d", used, native.leases.Load())
	}
}

func TestIndependentBridgeNativeFailureBypassesBlockedSendAndJoinsNativeClose(t *testing.T) {
	native := newIndependentBridgeNative()
	out := make(chan int)
	_, sub := independentStartBridge(t, native, out)
	independentBridgeWait(t, native.nextEntered)
	native.events <- 7
	independentBridgeWait(t, native.nextEntered)
	independentBridgeEventually(t, func() bool { return len(sub.queue) == 0 })
	// The only event has left the queue, while the caller never receives it.
	native.cause = errors.New("independent native producer failed")
	close(native.terminated)
	independentBridgeWait(t, native.closeEntered)
	select {
	case err := <-sub.Err():
		t.Fatalf("Err completed before delayed native Close: %v", err)
	case <-sub.Done():
		t.Fatal("Done completed before delayed native Close")
	default:
	}
	native.unblockClose()
	independentBridgeAssertSettled(t, native, sub, native.cause)
	select {
	case <-out:
		t.Fatal("blocked caller received an event after terminal")
	default:
	}
}

func TestIndependentBridgeFixedHandoffSaturationStopsAndReleasesEveryLease(t *testing.T) {
	native := newIndependentBridgeNative()
	out := make(chan int)
	_, sub := independentStartBridge(t, native, out)
	independentBridgeWait(t, native.nextEntered)
	native.events <- 1
	independentBridgeWait(t, native.nextEntered)
	independentBridgeEventually(t, func() bool { return len(sub.queue) == 0 })
	// One batch is held by the unread sender; exactly two further events fit.
	for value := 2; value <= 3; value++ {
		native.events <- value
		independentBridgeWait(t, native.nextEntered)
	}
	if len(sub.queue) != 2 || cap(sub.queue) != 2 {
		t.Fatal("bridge no longer uses the frozen two-event handoff")
	}
	native.events <- 4
	independentBridgeWait(t, native.closeEntered)
	native.unblockClose()
	independentBridgeAssertSettled(t, native, sub, ethguard.ErrHeadMemoryBudget)
}

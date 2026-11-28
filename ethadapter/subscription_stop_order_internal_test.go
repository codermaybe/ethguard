package ethadapter

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard"
)

type independentRejectedTransfer struct {
	*independentBridgeNative
	rejected    chan struct{}
	allowReturn chan struct{}
}

func (n *independentRejectedTransfer) NextWithBudget(_ context.Context, budget ethguard.SubscriptionMemoryBudget) (int, func(), error) {
	_, err := budget.Reserve(bridgeMemoryLimit + 1)
	close(n.rejected)
	<-n.allowReturn
	// A real Head transfer rejection can return audit under its original
	// source lease. Its release must remain owned even when another stop wins.
	n.leases.Add(1)
	return 0, sync.OnceFunc(func() { n.leases.Add(-1) }), err
}

func TestIndependentSubscriptionStopOrderKeepsEstablishedCauseAndReleasesRejectedTransfer(t *testing.T) {
	for _, first := range []string{"native_failure", "native_intentional", "adapter"} {
		t.Run(first, func(t *testing.T) {
			native := &independentRejectedTransfer{independentBridgeNative: newIndependentBridgeNative(), rejected: make(chan struct{}), allowReturn: make(chan struct{})}
			core, err := ethguard.Dial("http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			allow := sync.OnceFunc(func() { close(native.allowReturn) })
			t.Cleanup(func() { allow(); native.unblockClose(); core.Close() })
			audits := make(chan AuditEvent, 2)
			client := New(core, WithAudit(func(_ context.Context, ev AuditEvent) {
				if ev.Subscription.Phase == "terminal" {
					audits <- ev
				}
			}))
			subscription, err := startChannel(client, t.Context(), "independent rejected transfer", make(chan int), nil,
				func(context.Context) (nativeSubscription[int], error) { return native, nil },
				func(int) ([]int, ConversionReport, error) {
					t.Error("rejected payload reached conversion")
					return nil, ConversionReport{}, nil
				},
				func(int) *SubscriptionAudit { return &SubscriptionAudit{} })
			if err != nil {
				t.Fatal(err)
			}
			sub := subscription.(*channelSubscription[int])
			independentBridgeWait(t, native.rejected)
			var nativeCause error = errors.New("independent stable producer failure")
			if first == "native_intentional" {
				nativeCause = nil
			}
			if first != "adapter" {
				// Reservation failure alone has not established a bridge stop.
				// Producer failure becomes stable before Next returns that rejection.
				native.cause = nativeCause
				close(native.terminated)
				independentBridgeWait(t, sub.life.Done())
				allow()
				independentBridgeWait(t, native.closeEntered)
			} else {
				allow()
				independentBridgeWait(t, native.closeEntered)
				if native.leases.Load() != 1 {
					t.Fatal("winning transfer error released source audit before cleanup and Err handoff")
				}
				native.cause = nativeCause
				close(native.terminated)
			}
			native.unblockClose()
			independentBridgeWait(t, sub.Done())
			if native.leases.Load() != 0 {
				t.Fatal("Err handoff retained source audit lease")
			}
			want := ethguard.ErrHeadMemoryBudget
			if first != "adapter" {
				want = nativeCause
			}
			independentBridgeAssertSettled(t, native.independentBridgeNative, sub, want)
			audit := <-audits
			if first != "adapter" && (!errors.Is(audit.CoreErr, nativeCause) || audit.AdapterErr != nil) {
				t.Fatalf("stable producer cause was misclassified: %+v", audit)
			}
			if first == "adapter" && (!errors.Is(audit.AdapterErr, ethguard.ErrHeadMemoryBudget) || audit.CoreErr != nil) {
				t.Fatalf("established adapter rejection was replaced: %+v", audit)
			}
		})
	}
}

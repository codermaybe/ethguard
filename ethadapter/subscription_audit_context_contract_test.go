package ethadapter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/ethereum/go-ethereum/common"
)

type independentAuditContextKey struct{}
type independentAuditContextRecord struct {
	phase       string
	sameSetup   bool
	value       any
	err, cause  error
	deadline    bool
	donePresent bool
}

func TestIndependentSubscriptionAuditContextOutlivesSetupCancellation(t *testing.T) {
	for _, kind := range []string{"heads", "logs", "receipts"} {
		t.Run(kind, func(t *testing.T) {
			block := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
			peer := newBridgePeer(t, block)
			valued := context.WithValue(t.Context(), independentAuditContextKey{}, "independent trace")
			setup, cancel := context.WithTimeout(valued, time.Minute)
			defer cancel()
			audits := make(chan independentAuditContextRecord, 4)
			client := bridgeObservedClient(t, peer, func(ctx context.Context, ev ethadapter.AuditEvent) {
				_, deadline := ctx.Deadline()
				audits <- independentAuditContextRecord{phase: ev.Subscription.Phase, sameSetup: ctx == setup, value: ctx.Value(independentAuditContextKey{}), err: ctx.Err(), cause: context.Cause(ctx), deadline: deadline, donePresent: ctx.Done() != nil}
			})
			sink := newBridgeSink(kind, 0)
			sub, err := sink.subscribe(setup, client)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Unsubscribe()
			first := bridgeAwait(t, audits)
			if first.phase != "setup" || !first.sameSetup || first.value != "independent trace" || first.err != nil || !first.deadline || !first.donePresent {
				t.Fatalf("setup observer lost original context: %+v", first)
			}
			cancel()
			if !errors.Is(setup.Err(), context.Canceled) {
				t.Fatal("fixture setup context was not canceled")
			}
			peer.push(t, block)
			if sink.next(t) != block.header.Hash() {
				t.Fatal("setup cancellation changed stream lifetime")
			}
			event := bridgeAwait(t, audits)
			bridgeWaitClose(t, sub.Unsubscribe)
			bridgeTerminal(t, sub, nil)
			terminal := bridgeAwait(t, audits)
			for i, record := range []independentAuditContextRecord{event, terminal} {
				phase := []string{"event", "terminal"}[i]
				if record.phase != phase || record.value != "independent trace" || record.err != nil || record.cause != nil || record.deadline || record.donePresent {
					t.Errorf("%s observer retained construction lifetime instead of values: %+v", phase, record)
				}
			}
		})
	}
}

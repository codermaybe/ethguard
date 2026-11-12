package aggregator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// The pilot adapters' retained admission entry under Forwarded mode. A
// Forwarded round keeps the complete raw route — the frozen route decision
// never selects the processor for it — so the compiled plan's Admit hook
// must run with the frozen request and bind the response payload to the
// requested identity exactly as a non-forwarded round does. Losing that
// entry would let a forwarded round commit an object no request asked for.

// TestTransactionForwardedAdmissionBindingKept pins the binding: a
// Forwarded-mode by-hash round admits through the request's own hash, and a
// mismatched request is rejected by the same binding. The in-block pilot
// keeps its block-hash-and-index binding the same way.
func TestTransactionForwardedAdmissionBindingKept(t *testing.T) {
	raw, hash, blockHash, index := minedTransactionVector(t)
	other := strings.Repeat("22", 32)
	for _, method := range []string{methods.MethodGetTransactionByHash, methods.MethodGetTransactionByBlockHashAndIndex} {
		t.Run(method, func(t *testing.T) {
			counts := &workCounters{}
			plan := instrumentedPlan(t, method, counts)
			request := rpc.Request{Method: method, Params: []any{hash}}
			if method == methods.MethodGetTransactionByBlockHashAndIndex {
				request = rpc.Request{Method: method, Params: []any{blockHash, index}}
			}
			agg := forwardedFleet(t, method, plan, raw)
			decision, err := agg.Aggregate(context.Background(), request)
			if err != nil || decision.Status != rpc.Forwarded {
				t.Fatalf("forwarded round = %+v, %v", decision, err)
			}
			if counts.admit.Load() != 1 || counts.process.Load() != 0 {
				t.Fatalf("forwarded round admission entries = %d, processor entries = %d; want the admission entry and no processing", counts.admit.Load(), counts.process.Load())
			}
			// A mismatched identity goes through the same admission entry
			// and is rejected by the binding, not silently committed.
			mismatched := request
			if method == methods.MethodGetTransactionByHash {
				mismatched.Params = []any{"0x" + other}
			} else {
				mismatched.Params = []any{"0x" + other, index}
			}
			counts.reset()
			if _, err := agg.Aggregate(context.Background(), mismatched); !errors.Is(err, rpc.ErrMalformedResponse) {
				t.Fatalf("mismatched forwarded request = %v, want the binding's malformed class", err)
			}
			if counts.admit.Load() != 1 {
				t.Fatalf("mismatched round bypassed the admission entry: %d", counts.admit.Load())
			}
		})
	}
}

func (c *workCounters) reset() {
	c.admit.Store(0)
	c.encode.Store(0)
	c.normalize.Store(0)
	c.process.Store(0)
}

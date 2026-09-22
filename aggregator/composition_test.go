package aggregator

import (
	"context"
	"errors"
	"testing"

	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

func TestDecoratedForwardContactsOneSourceAndAcceptsProtocolAnswer(t *testing.T) {
	decorators := map[string]func(rpc.Policy) rpc.Policy{
		"hash": rpc.WithAnchor(nil), "height": rpc.WithAnchorHeight(nil),
		"fields": rpc.WithAnchorHeightFields(nil, 0, "fromBlock"), "pending": rpc.RejectPending(),
	}
	for name, decorate := range decorators {
		t.Run(name, func(t *testing.T) {
			a, b := rpctest.NewNode(1), rpctest.NewNode(1)
			defer a.Close()
			defer b.Close()
			a.Fail("custom_write", -32000, "authoritative answer")
			b.Return("custom_write", "unexpected")
			p := pool.NewPool()
			defer p.Close()
			p.Join("a", a.URL())
			p.Join("b", b.URL())
			rec := &captureRecorder{}
			agg := New(p, rpc.Registry{"custom_write": decorate(rpc.ForwardPolicy{})}, WithRecorder(rec))
			d, err := agg.Aggregate(context.Background(), rpc.Request{Method: "custom_write"})
			// Scheme B: an error return carries Status Unknown even when the
			// protocol answer is authoritative. Provenance stays on Support and
			// the attempt stays usable; no confidence class is claimed.
			if err == nil || d.Status != rpc.Unknown || len(d.Support) != 1 || len(d.Faults) != 0 {
				t.Fatalf("protocol answer changed: %+v %v", d, err)
			}
			if len(b.Calls()) != 0 || len(a.Calls()) != 1 {
				t.Fatalf("forward was broadcast: %d/%d", len(a.Calls()), len(b.Calls()))
			}
			if len(rec.events) != 1 || len(rec.events[0].Attempts) != 1 || rec.events[0].Attempts[0].Failed {
				t.Fatalf("authoritative answer counted as failure: %+v", rec.events)
			}
		})
	}
}
func TestEstimateFailureRetainsSourceCause(t *testing.T) {
	a, b := rpctest.NewNode(1), rpctest.NewNode(1)
	defer a.Close()
	defer b.Close()
	a.Return(methods.MethodEstimateGas, "0x5208")
	b.Fail(methods.MethodEstimateGas, -32000, "estimation rejected")
	p := pool.NewPool()
	defer p.Close()
	p.Join("good", a.URL())
	p.Join("bad", b.URL())
	rec := &captureRecorder{}
	d, err := New(p, rpc.Registry{methods.MethodEstimateGas: methods.EstimateGasPolicy(nil)}, WithRecorder(rec)).Aggregate(context.Background(), rpc.Request{Method: methods.MethodEstimateGas, Params: []any{map[string]any{}, "0x10"}})
	if err != nil || d.Status != rpc.Selected || len(d.Faults) != 1 {
		t.Fatalf("lost source cause: %+v %v", d, err)
	}
	var protocol interface{ ErrorCode() int }
	if !errors.As(d.Faults[0], &protocol) || protocol.ErrorCode() != -32000 {
		t.Fatalf("lost structured error: %v", d.Faults)
	}
	if rec.events[0].Faults != 1 || !rec.events[0].Attempts[1].Failed {
		t.Fatalf("inconsistent observation: %+v", rec.events)
	}
}

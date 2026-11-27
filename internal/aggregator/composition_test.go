package aggregator

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// TestForwardPlanContactsOneSourceAndAcceptsProtocolAnswer pins the explicit
// forward branch: one source is contacted, the selected provider's protocol
// answer is authoritative, and the attempt stays usable.
func TestForwardPlanContactsOneSourceAndAcceptsProtocolAnswer(t *testing.T) {
	a, b := rpctest.NewNode(1), rpctest.NewNode(1)
	defer a.Close()
	defer b.Close()
	a.Fail("custom_write", -32000, "authoritative answer")
	b.Return("custom_write", "unexpected")
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
		pool.Member{Name: "a", URL: a.URL()},
		pool.Member{Name: "b", URL: b.URL()},
	)
	rec := &captureRecorder{}
	agg := New(p, rpcengine.Corroboration{Floor: 1}, WithPlans(map[string]rpcengine.ReadPlan{"custom_write": {Method: "custom_write", Resolver: rpcengine.ForwardResolver{}}}), WithRecorder(rec))
	d, err := agg.Aggregate(context.Background(), rpc.Request{Method: "custom_write"})
	// A forwarded protocol answer keeps its formed status beside the error.
	if err == nil || d.Status != rpc.Forwarded || len(d.Support) != 1 || len(d.Faults) != 0 {
		t.Fatalf("protocol answer changed: %+v %v", d, err)
	}
	if len(b.Calls()) != 0 || len(a.Calls()) != 1 {
		t.Fatalf("forward was broadcast: %d/%d", len(a.Calls()), len(b.Calls()))
	}
	if len(rec.events) != 1 || len(rec.events[0].Attempts) != 1 || rec.events[0].Attempts[0].Failed {
		t.Fatalf("authoritative answer counted as failure: %+v", rec.events)
	}
}

// TestUnregisteredMethodForwards pins the explicit forward branch for a
// method outside the registry: one bounded attempt, Forwarded semantics, and
// no agreement claim.
func TestUnregisteredMethodForwards(t *testing.T) {
	a, b := rpctest.NewNode(1), rpctest.NewNode(1)
	defer a.Close()
	defer b.Close()
	a.Return("eth_unlisted", "0x64")
	b.Return("eth_unlisted", "0x64")
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
		pool.Member{Name: "a", URL: a.URL()},
		pool.Member{Name: "b", URL: b.URL()},
	)
	d, err := New(p, rpcengine.Corroboration{Floor: 1}, WithRecorder(&captureRecorder{})).Aggregate(context.Background(), rpc.Request{Method: "eth_unlisted"})
	if err != nil || d.Status != rpc.Forwarded {
		t.Fatalf("unregistered method = %+v %v, want a forwarded answer", d, err)
	}
	if len(d.Support) != 1 {
		t.Fatalf("support = %v, want the single forwarded source", d.Support)
	}
}

package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The two transaction routes through one real Aggregate, measured end to
// end: the compiled canonical route (processor per source, no raw hooks)
// against the same read forced onto the raw route (admission plus the
// resolver's canonical encoder per source, the normalizer never runs because
// the comparison key is the canonical bytes). Server setup and one warm-up
// invocation stay outside the timed loop; the per-op hook entries, B/op and
// allocs/op report what one invocation of each route really spends per
// source count, so any later shape change has a recorded baseline and an
// unmeasured claim cannot stand in for one.

// routeAggregateFleet installs one scripted fleet of n members over the
// method, all answering with the same official body.
func routeAggregateFleet(b *testing.B, method string, body json.RawMessage, n int) *pool.Pool {
	b.Helper()
	members := make([]pool.Member, 0, n)
	for i := range n {
		node := rpctest.NewNode(0)
		node.ReturnRaw(method, body)
		b.Cleanup(node.Close)
		members = append(members, pool.Member{Name: fmt.Sprintf("route-%d", i), URL: node.URL()})
	}
	p, err := pool.NewWithMembers(0, members, pool.WithDialer(nativehttp.NewDialer()))
	if err != nil {
		b.Fatalf("NewWithMembers: %v", err)
	}
	b.Cleanup(p.Close)
	return p
}

// routeAggregateCorroboration picks the floor the fleet size admits: one
// member resolves as a single-source Selected read, larger fleets
// corroborate.
func routeAggregateCorroboration(b *testing.B, sources int) *rpcengine.CorroborationShared {
	b.Helper()
	floor := 2
	if sources == 1 {
		floor = 1
	}
	shared, err := rpcengine.NewCorroborationShared(rpc.CorroborationMajority, floor)
	if err != nil {
		b.Fatal(err)
	}
	return shared
}

func BenchmarkTransactionRouteAggregate(b *testing.B) {
	const method = methods.MethodGetTransactionByHash
	raw, hash, _, _ := minedTransactionVector(b)
	request := rpc.Request{Method: method, Params: []any{hash}}
	for _, sources := range []int{1, 3, 5} {
		b.Run(fmt.Sprintf("sources-%d", sources), func(b *testing.B) {
			p := routeAggregateFleet(b, method, raw, sources)
			shared := routeAggregateCorroboration(b, sources)
			wantStatus := rpc.Corroborated
			if sources == 1 {
				wantStatus = rpc.Selected
			}
			for _, route := range []struct {
				name      string
				canonical bool
			}{
				{name: "canonical", canonical: true},
				{name: "raw"},
			} {
				b.Run(route.name, func(b *testing.B) {
					counts := &workCounters{}
					plan := instrumentedPlan(b, method, counts)
					if !route.canonical {
						plan.ProcessCanonical = nil
					}
					agg := New(p, WithPlans(map[string]rpcengine.ReadPlan{method: plan}), WithCorroboration(shared))
					warm, warmErr := agg.Aggregate(context.Background(), request)
					if warmErr != nil || warm.Status != wantStatus || len(warm.Support) != sources {
						b.Fatalf("warm-up = %v, %v (sources %d)", warm.Status, warmErr, sources)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						decision, aggErr := agg.Aggregate(context.Background(), request)
						if aggErr != nil || decision.Status != wantStatus || len(decision.Support) != sources {
							b.Fatalf("aggregate = %v, %v", decision.Status, aggErr)
						}
					}
					b.StopTimer()
					ops := float64(b.N)
					b.ReportMetric(float64(counts.process.Load())/ops, "processor-entries/op")
					b.ReportMetric(float64(counts.encode.Load())/ops, "encoder-entries/op")
					b.ReportMetric(float64(counts.normalize.Load())/ops, "normalizer-entries/op")
					b.ReportMetric(float64(counts.admit.Load())/ops, "admission-entries/op")
				})
			}
		})
	}
}

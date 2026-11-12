package ethguard_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// BenchmarkTransactionRouteComparison isolates the processing-route delta on
// the production assembler: the real compiled TransactionByHash plan driven
// through a real aggregator over scripted HTTP sources, once with the frozen
// canonical route (the shipped behavior) and once with the processor removed
// — the raw route, which is exactly the pre-refactor work shape (full
// admission preparation plus the cluster re-encode per source). Same fixture,
// same harness, same binary; the delta between the two routes is the
// processing change the seam bought. The harness is same-process HTTP, so
// absolute times carry no provider network meaning.
func BenchmarkTransactionRouteComparison(b *testing.B) {
	for _, route := range []string{"canonical", "raw"} {
		for _, count := range []int{3, 5} { // a two-source floor cannot be met by a single member
			b.Run(fmt.Sprintf("%s/sources=%d", route, count), func(b *testing.B) {
				plan, err := methods.PlanFor(methods.MethodGetTransactionByHash)
				if err != nil {
					b.Fatal(err)
				}
				if route == "raw" {
					plan.ProcessCanonical = nil
				}
				raw, hash := transactionBenchFixture(b, 128, 2)
				var members []pool.Member
				nodes := make([]*rpctest.Node, 0, count)
				for i := range count {
					node := rpctest.NewNode(1)
					node.ReturnRaw(methods.MethodGetTransactionByHash, raw)
					nodes = append(nodes, node)
					members = append(members, pool.Member{Name: fmt.Sprintf("bench-%d", i), URL: node.URL()})
				}
				b.Cleanup(func() {
					for _, node := range nodes {
						node.Close()
					}
				})
				p, err := pool.NewWithMembers(1, members, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}...)
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(p.Close)
				shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
				if err != nil {
					b.Fatal(err)
				}
				agg := aggregator.New(p, aggregator.WithPlans(map[string]rpc.ReadPlan{methods.MethodGetTransactionByHash: plan}), aggregator.WithCorroboration(shared))
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				request := rpc.Request{Method: methods.MethodGetTransactionByHash, Params: []any{hash.Hex()}}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					decision, err := agg.Aggregate(ctx, request)
					if err != nil || decision.Status != rpc.Corroborated {
						b.Fatalf("round = %+v, %v", decision, err)
					}
				}
			})
		}
	}
}

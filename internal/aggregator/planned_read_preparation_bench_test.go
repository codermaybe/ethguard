package aggregator

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The prepared-read preparation baseline: what one planned invocation spends
// on the preparation pipeline — the one controlled serialization, the
// reference rewrite, the frozen routing facts, and the per-source work the
// frozen route selects — measured over a scripted same-process fleet.
//
// Two routes are measured separately. The probe read runs the full prepare
// order (establishment through the scripted stage, pin rewrite, freeze) on
// the raw strict route; the transaction read runs the compiled canonical
// route with its processor. The counting witnesses report the facts the
// freeze contract cares about per operation: the user marshaler must run
// exactly once per read regardless of the source count, and the per-source
// hook entries (processor, canonical encoder, normalizer, admission) follow
// the frozen route — never both routes' hooks. Absolute times carry no
// provider-network meaning; the value is the per-op work shape and the
// allocation profile of the preparation pipeline itself.

// preparationCounters is one invocation-witness set for the benchmark loops.
type preparationCounters struct {
	marshaler atomic.Int64
	admit     atomic.Int64
	encode    atomic.Int64
	normalize atomic.Int64
	process   atomic.Int64
}

// countedMarshaler is a parameter whose serialization is witnessed; a
// nondeterministic second serialization would fail the whole benchmark, so
// the bytes stay stable and the count does the talking.
type countedMarshaler struct {
	counts *preparationCounters
}

func (m countedMarshaler) MarshalJSON() ([]byte, error) {
	m.counts.marshaler.Add(1)
	return []byte(`"0x1"`), nil
}

// preparationFleet builds a scripted same-process fleet of n members.
func preparationFleet(b *testing.B, n int) ([]*rpctest.Node, *pool.Pool) {
	b.Helper()
	nodes := make([]*rpctest.Node, n)
	members := make([]pool.Member, 0, n)
	for i := range nodes {
		node := rpctest.NewNode(1)
		b.Cleanup(node.Close)
		nodes[i] = node
		members = append(members, pool.Member{Name: string(rune('a' + i)), URL: node.URL()})
	}
	p := fixedPoolWith(b, 1, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	return nodes, p
}

// probePreparationAggregator assembles the probe read over the scripted
// establishment stage: the full prepare order runs on every invocation.
func probePreparationAggregator(b *testing.B, p *pool.Pool) *Aggregator {
	b.Helper()
	shared, err := rpcengine.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		b.Fatal(err)
	}
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		b.Fatal(err)
	}
	return New(p,
		WithCorroboration(shared),
		WithCollectTimeout(2*time.Second),
		WithReferenceStage(&scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}),
		WithPlans(map[string]rpcengine.ReadPlan{probeMethod: plan}))
}

// transactionPreparationAggregator assembles the transaction read with
// counting wrappers over the compiled plan's own hooks.
func transactionPreparationAggregator(b *testing.B, p *pool.Pool, counts *preparationCounters) *Aggregator {
	b.Helper()
	spec, ok := methods.SpecFor(methods.MethodGetTransactionByHash)
	if !ok {
		b.Fatal("eth_getTransactionByHash is not registered")
	}
	if spec.Admit != nil {
		original := spec.Admit
		spec.Admit = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (json.RawMessage, error) {
			counts.admit.Add(1)
			return original(ctx, req, value, caller)
		}
	}
	if spec.Encode != nil {
		original := spec.Encode
		spec.Encode = func(value json.RawMessage) (json.RawMessage, error) {
			counts.encode.Add(1)
			return original(value)
		}
	}
	if spec.Normalize != nil {
		original := spec.Normalize
		spec.Normalize = func(value json.RawMessage) (string, error) {
			counts.normalize.Add(1)
			return original(value)
		}
	}
	if spec.ProcessCanonical != nil {
		original := spec.ProcessCanonical
		spec.ProcessCanonical = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			counts.process.Add(1)
			return original(ctx, req, value, caller)
		}
	}
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		b.Fatalf("instrumented transaction plan does not compile: %v", err)
	}
	shared, err := rpcengine.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		b.Fatal(err)
	}
	return New(p, WithCorroboration(shared), WithPlans(map[string]rpcengine.ReadPlan{methods.MethodGetTransactionByHash: plan}))
}

// upstreamRequestsBefore snapshots the fleet's request count so the loop can
// report the per-operation upstream request volume alongside the timings.
func upstreamRequestsBefore(nodes []*rpctest.Node) int {
	total := 0
	for _, node := range nodes {
		total += len(node.Calls())
	}
	return total
}

// BenchmarkPlannedReadPreparation runs the two preparation routes over a
// three-member fleet: the probe read exercises the full prepare order with a
// witnessed marshaler, the transaction read exercises the compiled canonical
// route with witnessed per-source hooks.
func BenchmarkPlannedReadPreparation(b *testing.B) {
	const sources = 3
	nodes, p := preparationFleet(b, sources)

	b.Run("probe-rewrite-strict", func(b *testing.B) {
		for _, node := range nodes {
			node.Return(probeMethod, "0x64")
		}
		counts := &preparationCounters{}
		agg := probePreparationAggregator(b, p)
		request := rpc.Request{Method: probeMethod, Params: []any{countedMarshaler{counts: counts}, "latest"}}
		decision, err := agg.Aggregate(context.Background(), request)
		if err != nil || decision.Status != rpc.Corroborated || decision.Prepared == nil {
			b.Fatalf("warm-up = %v, %v", decision.Status, err)
		}
		before := upstreamRequestsBefore(nodes)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			decision, aggErr := agg.Aggregate(context.Background(), request)
			if aggErr != nil || decision.Status != rpc.Corroborated {
				b.Fatalf("aggregate = %v, %v", decision.Status, aggErr)
			}
		}
		b.StopTimer()
		ops := float64(b.N)
		b.ReportMetric(float64(counts.marshaler.Load())/ops, "marshaler-runs/op")
		b.ReportMetric(float64(upstreamRequestsBefore(nodes)-before)/ops, "upstream-requests/op")
	})

	b.Run("transaction-canonical-route", func(b *testing.B) {
		counts := &preparationCounters{}
		agg := transactionPreparationAggregator(b, p, counts)
		raw, hash, _, _ := minedTransactionVector(b)
		for _, node := range nodes {
			node.ReturnRaw(methods.MethodGetTransactionByHash, raw)
		}
		request := rpc.Request{Method: methods.MethodGetTransactionByHash, Params: []any{hash}}
		decision, err := agg.Aggregate(context.Background(), request)
		if err != nil || decision.Status != rpc.Corroborated || decision.Prepared == nil {
			b.Fatalf("warm-up = %v, %v", decision.Status, err)
		}
		before := upstreamRequestsBefore(nodes)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			decision, aggErr := agg.Aggregate(context.Background(), request)
			if aggErr != nil || decision.Status != rpc.Corroborated {
				b.Fatalf("aggregate = %v, %v", decision.Status, aggErr)
			}
		}
		b.StopTimer()
		ops := float64(b.N)
		b.ReportMetric(float64(counts.marshaler.Load())/ops, "marshaler-runs/op")
		b.ReportMetric(float64(counts.process.Load())/ops, "processor-entries/op")
		b.ReportMetric(float64(counts.encode.Load())/ops, "encoder-entries/op")
		b.ReportMetric(float64(counts.normalize.Load())/ops, "normalizer-entries/op")
		b.ReportMetric(float64(counts.admit.Load())/ops, "admission-entries/op")
		b.ReportMetric(float64(upstreamRequestsBefore(nodes)-before)/ops, "upstream-requests/op")
	})
}

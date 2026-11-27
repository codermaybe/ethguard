package runtime

import (
	"context"

	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpctest"
)

// The reference establishment baseline: what one facade establishment costs
// end to end over a scripted same-process fleet. Absolute times carry no
// provider-network meaning — the HTTP transport dominates — so the value is
// the request shape and the allocation profile of the establishment handoff:
// one voting head round per establishment, on success and on failure alike,
// with the failure row also delivering the evidence the round formed.

// establishmentFleet builds a three-member fleet whose head endpoint answers
// through the given handler for the whole benchmark.
func establishmentFleet(b *testing.B, serve rpctest.Handler) ([]*rpctest.Node, *pool.Pool) {
	b.Helper()
	nodes := make([]*rpctest.Node, 3)
	members := make([]pool.Member, 0, len(nodes))
	for i := range nodes {
		node := rpctest.NewNode(1)
		b.Cleanup(node.Close)
		node.Handle("eth_getBlockByNumber", serve)
		nodes[i] = node
		members = append(members, pool.Member{Name: fmt.Sprintf("bench-%d", i), URL: node.URL()})
	}
	fleet, err := pool.NewWithMembers(0, members, pool.WithDialer(nativehttp.NewDialer()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(fleet.Close)
	return nodes, fleet
}

// establishmentBenchmark runs one strict head establishment per iteration
// through the facade service — the same entry the composed client exposes —
// and reports the upstream request count per operation alongside the timing.
func establishmentBenchmark(b *testing.B, serve rpctest.Handler) {
	b.Helper()
	nodes, fleet := establishmentFleet(b, serve)
	stage := NewReferenceStage(ReferenceStageConfig{
		Pool:     fleet,
		Timeout:  2 * time.Second,
		Recorder: observe.Nop,
	})
	service := NewStrictHeadService(stage, rpcengine.Corroboration{Floor: 2})

	nodeCalls := func(nodes []*rpctest.Node) int {
		sent := 0
		for _, node := range nodes {
			sent += len(node.Calls())
		}
		return sent
	}
	before := nodeCalls(nodes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.Reference(context.Background())
	}
	b.StopTimer()
	b.ReportMetric(float64(nodeCalls(nodes)-before)/float64(b.N), "requests/op")
}

func BenchmarkStrictHeadReferenceEstablishment(b *testing.B) {
	establishmentBenchmark(b, func(rpctest.Call) (any, error) {
		return map[string]any{"number": "0x64", "hash": "0xabc"}, nil
	})
}

func BenchmarkStrictHeadReferenceEstablishmentFailure(b *testing.B) {
	// A structurally delivered head the establishment's codec rejects: the
	// HTTP exchange stays healthy, so the pool's health gate never pauses the
	// sources and every iteration runs one real voting round — measuring the
	// failed round's fault assembly and evidence delivery, not a transport
	// backoff.
	establishmentBenchmark(b, func(rpctest.Call) (any, error) {
		return map[string]any{"number": "wrong", "hash": "0xabc"}, nil
	})
}

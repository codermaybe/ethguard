package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// tipScopedFleet builds three established sources whose sampled latest tips
// are 100/100/10. The third source is far behind, so it is the observation
// that distinguishes a latest-scoped round from a safe/finalized round.
func tipScopedFleet(t *testing.T) (*pool.Pool, []*rpctest.Node, []*pool.Source, rpc.Corroboration) {
	t.Helper()
	heights := []string{"0x64", "0x64", "0xa"}
	nodes := make([]*rpctest.Node, len(heights))
	members := make([]pool.Member, 0, len(heights))
	for i, height := range heights {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		node.Handle("eth_blockNumber", rpctest.Value(height))
		node.Handle("eth_getBlockByNumber", rpctest.Value(map[string]any{
			"number": fmt.Sprintf("0x%x", 100),
			"hash":   "0xabc",
		}))
		nodes[i] = node
		members = append(members, pool.Member{Name: fmt.Sprintf("node-%d", i), URL: node.URL()})
	}
	p, err := pool.NewWithMembers(1, members,
		pool.WithDialer(nativehttp.NewDialer()),
		pool.WithTipTracking(time.Hour, 16))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	sources := p.VotingMembers()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, pool.CollectOptions{}); err != nil {
		t.Fatalf("warm sources: %v", err)
	}
	p.RefreshTips(ctx)
	for i, source := range sources {
		want := uint64(100)
		if i == 2 {
			want = 10
		}
		health := source.Health()
		if health.TipObservedAt.IsZero() || health.TipHeight != want {
			t.Fatalf("source %d tip = %d observed %v, want %d", i, health.TipHeight, health.TipObservedAt, want)
		}
	}

	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	return p, nodes, sources, shared.Snapshot()
}

func blockCallCounts(nodes []*rpctest.Node) []int {
	counts := make([]int, len(nodes))
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == "eth_getBlockByNumber" {
				counts[i]++
			}
		}
	}
	return counts
}

// TestReferenceRoundTipScopeFollowsTag proves the establishment round does not
// apply a latest-tip gate to safe/finalized reads: latest pauses the lagging
// source, while safe and finalized still contact it. The round's members,
// basis, and thresholds are unchanged.
func TestReferenceRoundTipScopeFollowsTag(t *testing.T) {
	for _, tc := range []struct {
		tag       string
		wantCalls int
	}{
		{"latest", 0},
		{"safe", 1},
		{"finalized", 1},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			p, nodes, sources, snap := tipScopedFleet(t)
			engine := NewStrictHeadEstablishment(p, observe.Nop, 2*time.Second)
			before := blockCallCounts(nodes)
			_, err := engine.establish(context.Background(), sources, snap, true, tc.tag)
			if err != nil {
				t.Fatalf("establish %s: %v", tc.tag, err)
			}
			after := blockCallCounts(nodes)
			got := after[2] - before[2]
			if got != tc.wantCalls {
				t.Fatalf("%s round contacted the lagging source %d times, want %d", tc.tag, got, tc.wantCalls)
			}
		})
	}
}

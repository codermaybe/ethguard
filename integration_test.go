package ethguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	promclient "github.com/prometheus/client_golang/prometheus"

	"ethguard"
	"ethguard/config"
	dprom "ethguard/observe/prometheus"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

// fleet spins up count fake nodes answering eth_chainId with nodeChainID by
// default, wires them into one client through the public configuration path,
// and configures the pool to enforce poolChainID (zero disables the check).
// Extra opts tune the client like any other construction.
func fleet(t *testing.T, nodeChainID, poolChainID uint64, count int, opts ...ethguard.Option) (*ethguard.Client, []*rpctest.Node) {
	t.Helper()
	nodes := make([]*rpctest.Node, 0, count)
	cfg := config.Config{ChainID: poolChainID}
	for i := range count {
		node := rpctest.NewNode(nodeChainID)
		t.Cleanup(node.Close)
		nodes = append(nodes, node)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%c", 'a'+i),
			Endpoint: node.URL(),
		})
	}
	client, err := ethguard.New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, nodes
}

// balancePinnedAt scripts every node to answer balances that agree only at
// the given hash and diverge for any other reference (including moving tags).
func balancePinnedAt(nodes []*rpctest.Node, hash string) {
	for i, node := range nodes {
		node.Handle("eth_getBalance", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == hash {
				return "0x64", nil // agreed balance at the anchored block
			}
			return fmt.Sprintf("0x%x", 100+i), nil // divergent answers elsewhere
		})
	}
}

// agreeOnHead scripts every node into one common latest-block head, with
// balances that agree only at the pinned hash.
func agreeOnHead(nodes []*rpctest.Node) {
	head := map[string]any{"number": "0x10", "hash": "0xabc"}
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", head)
	}
	balancePinnedAt(nodes, "0xabc")
}

// scriptFinalizedHeads answers the anchor pipeline's finalized-tag query
// per node (finalized(i)) while the plain latest tag returns latest(i).
func scriptFinalizedHeads(nodes []*rpctest.Node, latest, finalized func(i int) map[string]any) {
	for i, node := range nodes {
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) > 0 {
				json.Unmarshal(c.Params[0], &tag)
			}
			if tag == "finalized" {
				return finalized(i), nil
			}
			return latest(i), nil
		})
	}
}

func TestClientVerifiesChainIDAcrossSources(t *testing.T) {
	client, _ := fleet(t, 1, 1, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, decision, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.Int64() != 1 {
		t.Fatalf("expected chain id 1, got %v", value)
	}
	if decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected verified decision with three supporters, got %+v", decision)
	}
}

func TestClientReportsDeterministicConflictEvidenceOnDissent(t *testing.T) {
	// Chain validation is disabled (pool side zero) so a disagreeing provider
	// reaches the aggregation layer instead of being rejected up front.
	client, nodes := fleet(t, 1, 0, 3)
	nodes[2].Return("eth_chainId", "0x2")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, decision, err := client.ChainID(ctx)
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if len(decision.Support) != 2 || len(decision.Reject) != 1 {
		t.Fatalf("expected 2 support and 1 reject, got %+v", decision)
	}
	if string(decision.Value) != `"0x1"` {
		t.Fatalf("expected majority value in evidence, got %s", decision.Value)
	}
}

func TestClientSurvivesHungSourceWithFaultEvidence(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	nodes[1].Hang("eth_chainId", 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	value, decision, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.Int64() != 1 || decision.Status != rpc.Verified || len(decision.Support) != 2 {
		t.Fatalf("expected verified id 1 from two healthy sources, got %v %+v", value, decision)
	}
	if len(decision.Faults) == 0 {
		t.Fatal("expected hung source to appear in faults")
	}
}

func TestClientQuarantinesPoisonedChainIdentity(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	// One provider reports another chain than configured: the pool rejects it
	// while the remaining two sources still verify cleanly.
	nodes[1].Handle("eth_chainId", rpctest.Value("0x5"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, decision, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.Int64() != 1 {
		t.Fatalf("expected chain id from honest sources, got %v", value)
	}
	if len(decision.Faults) == 0 {
		t.Fatal("expected poisoned provider to be reported in faults")
	}
}

func TestClientAnchorsBalanceReadsAcrossDivergentHeads(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	// All providers agree on one head for anchoring purposes, but report
	// different balances for a moving tag — noise without the anchor.
	agreeOnHead(nodes)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected anchored read to verify across three sources, got %+v", decision)
	}

	// The rewrite must be visible on the wire: every node was asked for the
	// pinned hash, never for a moving tag.
	for i, node := range nodes {
		var sawAnchor bool
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBalance || len(call.Params) < 2 {
				continue
			}
			var tag string
			if err := json.Unmarshal(call.Params[1], &tag); err != nil {
				t.Fatalf("node %d received malformed tag %s", i, call.Params[1])
			}
			if tag == "latest" || tag == "safe" || tag == "finalized" {
				t.Fatalf("node %d received an unrewritten moving tag %q", i, tag)
			}
			if tag == "0xabc" {
				sawAnchor = true
			}
		}
		if !sawAnchor {
			t.Fatalf("node %d was never asked for the anchor", i)
		}
	}
}

func TestClientFailsWhenHeadsDivergeForAnchoring(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	nodes[0].Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
	nodes[1].Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
	nodes[2].Return("eth_getBlockByNumber", map[string]any{"number": "0xf", "hash": "0xdifferent"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("expected anchor failure on divergent heads, got %v", err)
	}
	if got := strings.Count(err.Error(), "anchor:"); got != 1 {
		t.Fatalf("expected exactly one anchor prefix, got %d in %q", got, err)
	}
}

func TestClientNormalizesBlockResponsesAcrossSources(t *testing.T) {
	// Providers spell hexadecimal differently; the method policy must treat
	// both answers as one canonical value.
	client, nodes := fleet(t, 1, 1, 2)
	nodes[0].Handle("eth_getBlockByHash", rpctest.Value(map[string]any{
		"hash": "0xAB", "number": "0x1", "transactions": []any{},
	}))
	nodes[1].Handle("eth_getBlockByHash", rpctest.Value(map[string]any{
		"hash": "0xab", "number": "0x1", "transactions": []any{},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodGetBlockByHash, "0xab", false)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Verified || len(decision.Support) != 2 {
		t.Fatalf("expected cross-spelling agreement to verify, got %+v", decision)
	}
}

func TestClientRejectsBlockThatDoesNotMatchRequestedHash(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, json.RawMessage(pinned))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("42", 32))
	if _, _, err := client.BlockByHash(ctx, requested, true); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested block, got %v", err)
	}
}

func TestClientClassifiesFailuresThroughPublicSentinels(t *testing.T) {
	t.Run("closed client is pool closed", func(t *testing.T) {
		client, _ := fleet(t, 1, 1, 1)
		client.Close()

		_, err := client.Call(context.Background(), methods.MethodChainID)
		if !errors.Is(err, ethguard.ErrPoolClosed) {
			t.Fatalf("expected ErrPoolClosed, got %v", err)
		}
	})

	t.Run("dead fleet is all sources down", func(t *testing.T) {
		client, err := ethguard.New(config.Config{
			Upstreams: []config.Upstream{
				{ID: "dead-a", Endpoint: "http://127.0.0.1:1"},
				{ID: "dead-b", Endpoint: "http://127.0.0.1:2"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = client.Call(ctx, methods.MethodChainID)
		if !errors.Is(err, ethguard.ErrAllSourcesDown) {
			t.Fatalf("expected ErrAllSourcesDown, got %v", err)
		}
	})

	t.Run("all poisoned fleet is chain ID mismatch", func(t *testing.T) {
		client, _ := fleet(t, 5, 1, 3)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.Call(ctx, methods.MethodChainID)
		if !errors.Is(err, ethguard.ErrChainIDMismatch) {
			t.Fatalf("expected ErrChainIDMismatch, got %v", err)
		}
	})

	t.Run("JSON-RPC errors remain no valid response", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		for _, node := range nodes {
			node.Fail(methods.MethodGetBlockByHash, -32601, "method unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.Call(ctx, methods.MethodGetBlockByHash, common.Hash{}.Hex(), false)
		if !errors.Is(err, ethguard.ErrNoValidResponse) {
			t.Fatalf("expected ErrNoValidResponse, got %v", err)
		}
	})

	t.Run("dissent is conflict", func(t *testing.T) {
		client, nodes := fleet(t, 1, 0, 3)
		nodes[2].Return("eth_chainId", "0x2")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("expected ErrConflict, got %v", err)
		}
	})

	t.Run("fleet of unusable payloads is no valid response", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		for _, node := range nodes {
			node.Return("eth_getBlockByNumber", "not-a-block")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		decision, err := client.Call(ctx, methods.MethodGetBlockByNumber, "0x10", false)
		if !errors.Is(err, ethguard.ErrNoValidResponse) {
			t.Fatalf("expected ErrNoValidResponse, got %v", err)
		}
		if len(decision.Faults) == 0 {
			t.Fatal("expected per-source normalize faults on decision")
		}
	})

	t.Run("typed decode miss is malformed response", func(t *testing.T) {
		client, _ := fleet(t, 1, 1, 2)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, err := ethguard.CallInto[struct{ X int }](ctx, client, methods.MethodChainID)
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("expected ErrMalformedResponse, got %v", err)
		}
	})

	t.Run("poisoned identity rides on faults", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		nodes[1].Return("eth_chainId", "0x5")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, decision, err := client.ChainID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		poisoned := false
		for _, fault := range decision.Faults {
			if errors.Is(fault, ethguard.ErrChainIDMismatch) {
				poisoned = true
			}
		}
		if !poisoned {
			t.Fatalf("expected chain-ID mismatch on faults, got %+v", decision.Faults)
		}
	})
}

func TestClientEmitsOneDecisionEventToLogger(t *testing.T) {
	var buf bytes.Buffer
	client, _ := fleet(t, 1, 1, 3, ethguard.WithLogger(slog.New(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := client.ChainID(ctx); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if got := strings.Count(out, "msg=\"ethguard decision\""); got != 1 {
		t.Fatalf("expected exactly one decision event, got %d in:\n%s", got, out)
	}
	for _, want := range []string{"method=eth_chainId", "status=verified", "conflict=false", "sources=3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in event:\n%s", want, out)
		}
	}
}

// TestRecorderWinsOverLogger pins the documented priority: when both sinks
// are wired, the recorder receives the events and the logger stays silent.
func TestRecorderWinsOverLogger(t *testing.T) {
	pm := dprom.New()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, _ := fleet(t, 1, 1, 3,
		ethguard.WithLogger(logger), ethguard.WithRecorder(pm))
	reg := promclient.NewRegistry()
	if err := reg.Register(pm); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := client.ChainID(ctx); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); out != "" {
		t.Fatalf("logger must stay silent when a recorder is wired, got:\n%s", out)
	}
	if got := sampleValue(t, reg, "ethguard_decisions_total",
		map[string]string{"method": "eth_chainId", "status": "verified"}); got != 1 {
		t.Fatalf("recorder should have received the event, decisions_total = %v", got)
	}
}

// sampleValue returns the value of the sample named name carrying exactly the
// given labels, gathered from reg.
func sampleValue(t *testing.T, reg *promclient.Registry, name string, want map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}
			if len(labels) != len(want) {
				continue
			}
			match := true
			for key, value := range want {
				if labels[key] != value {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			return metric.GetCounter().GetValue()
		}
	}
	t.Fatalf("no sample %v{%s}", want, name)
	return 0
}

func TestClientExposesDecisionsThroughPrometheus(t *testing.T) {
	pm := dprom.New()
	// Identity check off so dissent reaches aggregation.
	client, nodes := fleet(t, 1, 0, 3, ethguard.WithRecorder(pm))

	reg := promclient.NewRegistry()
	if err := reg.Register(pm); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := client.ChainID(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sampleValue(t, reg, "ethguard_decisions_total",
		map[string]string{"method": "eth_chainId", "status": "verified"}); got != 1 {
		t.Fatalf("decisions_total{verified} = %v, want 1", got)
	}

	nodes[2].Return("eth_chainId", "0x2")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if _, _, err := client.ChainID(ctx2); !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("expected conflict on second call, got %v", err)
	}
	if got := sampleValue(t, reg, "ethguard_conflicts_total",
		map[string]string{"method": "eth_chainId"}); got != 1 {
		t.Fatalf("conflicts_total = %v, want 1", got)
	}
	if got := sampleValue(t, reg, "ethguard_decisions_total",
		map[string]string{"method": "eth_chainId", "status": "failed"}); got != 1 {
		t.Fatalf("decisions_total{failed} = %v, want 1", got)
	}

	// Per-source RTT must be observable per configured source name.
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var rttSeries int
	for _, family := range families {
		if family.GetName() != "ethguard_source_rtt_seconds" {
			continue
		}
		rttSeries += len(family.GetMetric())
	}
	if rttSeries != 3 {
		t.Fatalf("expected one rtt series per source, got %d", rttSeries)
	}
}

// TestAnchorEventsReportUnderBoundedLabel pins decision 0008: anchor
// establishment is observable under the fixed "anchor" literal, never under
// the wire method it fans out with.
func TestAnchorEventsReportUnderBoundedLabel(t *testing.T) {
	pm := dprom.New()
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithRecorder(pm))
	agreeOnHead(nodes)
	reg := promclient.NewRegistry()
	if err := reg.Register(pm); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest"); err != nil {
		t.Fatal(err)
	}
	if got := sampleValue(t, reg, "ethguard_decisions_total",
		map[string]string{"method": "anchor", "status": "verified"}); got != 1 {
		t.Fatalf("decisions_total{anchor,verified} = %v, want 1", got)
	}

	nodes[2].Return("eth_getBlockByNumber", map[string]any{"number": "0xf", "hash": "0xdifferent"})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if _, err := client.Call(ctx2, methods.MethodGetBalance, "0xaddr", "latest"); !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("expected anchor conflict on divergent heads, got %v", err)
	}
	if got := sampleValue(t, reg, "ethguard_decisions_total",
		map[string]string{"method": "anchor", "status": "failed"}); got != 1 {
		t.Fatalf("decisions_total{anchor,failed} = %v, want 1", got)
	}
	if got := sampleValue(t, reg, "ethguard_conflicts_total",
		map[string]string{"method": "anchor"}); got != 1 {
		t.Fatalf("conflicts_total{anchor} = %v, want 1", got)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, pair := range metric.GetLabel() {
				if pair.GetName() == "method" && pair.GetValue() == methods.MethodGetBlockByNumber {
					t.Fatalf("anchoring leaked onto wire-method label in %s", family.GetName())
				}
			}
		}
	}
}

// TestFinalizedAnchoringToleratesDivergentTips proves the availability gain
// of conservative selection: sources sitting on different latest heads would
// fail strict anchoring, yet agree on finalized state, so the read proceeds
// pinned to the agreed finalized hash.
func TestFinalizedAnchoringToleratesDivergentTips(t *testing.T) {
	pm := dprom.New()
	client, nodes := fleet(t, 1, 1, 3,
		ethguard.WithRecorder(pm), ethguard.WithFinalizedAnchoring())
	scriptFinalizedHeads(nodes,
		func(i int) map[string]any {
			return map[string]any{"number": fmt.Sprintf("0x%x", 0x65+i), "hash": fmt.Sprintf("0xtip%d", i)}
		},
		func(int) map[string]any { return map[string]any{"number": "0x64", "hash": "0xabc"} })
	balancePinnedAt(nodes, "0xabc")
	reg := promclient.NewRegistry()
	if err := reg.Register(pm); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if err != nil {
		t.Fatalf("expected divergent tips to anchor at agreed finalized head, got %v", err)
	}
	if decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected anchored read to verify across three sources, got %+v", decision)
	}
	if got := sampleValue(t, reg, "ethguard_decisions_total",
		map[string]string{"method": "anchor", "status": "verified"}); got != 1 {
		t.Fatalf("decisions_total{anchor,verified} = %v, want 1", got)
	}

	// The rewrite is on the wire: pinned hash in, no moving tag anywhere.
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBalance {
				continue
			}
			var tag string
			if err := json.Unmarshal(call.Params[1], &tag); err == nil && (tag == "latest" || tag == "safe" || tag == "finalized") {
				t.Fatalf("node received an unrewritten moving tag %q", tag)
			}
		}
	}
}

// TestFinalizedAnchoringIsolatesStaleReporter proves the freshness bound:
// a provider whose finalized view trails far behind is excluded from pinning
// the reference and surfaces as an anchor fault, without blocking the read.
func TestFinalizedAnchoringIsolatesStaleReporter(t *testing.T) {
	pm := dprom.New()
	client, nodes := fleet(t, 1, 1, 3,
		ethguard.WithRecorder(pm), ethguard.WithFinalizedAnchoring())
	scriptFinalizedHeads(nodes,
		func(int) map[string]any { return map[string]any{"number": "0x66", "hash": "0xtip"} },
		func(i int) map[string]any {
			if i == 2 {
				return map[string]any{"number": "0x2", "hash": "0xstale"} // 100 blocks behind
			}
			return map[string]any{"number": "0x66", "hash": "0xabc"}
		})
	balancePinnedAt(nodes, "0xabc")
	reg := promclient.NewRegistry()
	if err := reg.Register(pm); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest"); err != nil {
		t.Fatal(err)
	}
	if got := sampleValue(t, reg, "ethguard_faults_total",
		map[string]string{"method": "anchor"}); got != 1 {
		t.Fatalf("expected the stale reporter as one anchor fault, faults_total = %v", got)
	}
}

// TestFinalizedAnchoringRejectsForkAtPinnedHeight proves dissent at the
// chosen height still fails closed: two hashes claimed for one finalized
// height are a fork signal, not something to vote over.
func TestFinalizedAnchoringRejectsForkAtPinnedHeight(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithFinalizedAnchoring())
	scriptFinalizedHeads(nodes,
		func(int) map[string]any { return map[string]any{"number": "0x66", "hash": "0xtip"} },
		func(i int) map[string]any {
			if i < 2 {
				return map[string]any{"number": "0x64", "hash": "0xaaa"}
			}
			return map[string]any{"number": "0x64", "hash": "0xbbb"}
		})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("expected conflict on finalized fork, got %v", err)
	}
	if got := strings.Count(err.Error(), "anchor:"); got != 1 {
		t.Fatalf("expected exactly one anchor prefix, got %d in %q", got, err)
	}
	// Fail-before-fan-out: the balance read must never reach the wire.
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBalance {
				t.Fatalf("node %d received a balance read despite unresolved anchor", i)
			}
		}
	}
}

// TestTipTrackingQuarantinesLaggingSource proves the freshness gate end to
// end: a provider trailing the fleet's reference height is skipped by
// ordinary fan-outs while still receiving probes that can readmit it.
func TestTipTrackingQuarantinesLaggingSource(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithTipTracking(time.Hour, 16))
	for i, node := range nodes {
		height := fmt.Sprintf("0x%x", 100)
		if i == 2 {
			height = "0x10" // 80+ blocks behind
		}
		node.Return("eth_blockNumber", height)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First contact warms every source (probes never dial); one plain read
	// also proves the gate stays off until tips exist.
	if _, _, err := client.ChainID(ctx); err != nil {
		t.Fatal(err)
	}
	client.Pool().RefreshTips(ctx)
	seenByLaggard := len(nodes[2].Calls())

	value, decision, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.Int64() != 1 || decision.Status != rpc.Verified {
		t.Fatalf("expected verified chain id from fresh sources, got %v %+v", value, decision)
	}
	if len(decision.Support) != 2 {
		t.Fatalf("expected the lagging source to be gated out, got %+v", decision.Support)
	}
	for _, call := range nodes[2].Calls()[seenByLaggard:] {
		if call.Method == methods.MethodChainID {
			t.Fatal("gated source received a user request")
		}
	}

	// Recovery: catching up plus one probe readmits the source.
	nodes[2].Return("eth_blockNumber", "0x65")
	client.Pool().RefreshTips(ctx)
	if _, decision, err := client.ChainID(ctx); err != nil || len(decision.Support) != 3 {
		t.Fatalf("expected recovered source to be readmitted, got %+v %v", decision.Support, err)
	}
}

// anchorEstablishments counts block-head queries across the whole fleet:
// the wire footprint of anchor establishment.
func anchorEstablishments(nodes []*rpctest.Node) int {
	total := 0
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBlockByNumber {
				total++
			}
		}
	}
	return total
}

// TestAnchorCachingSharesOneEstablishment proves the latency win on the
// wire: consecutive anchored reads reuse one verified reference instead of
// re-fanning the head query per call.
func TestAnchorCachingSharesOneEstablishment(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithAnchorCaching(time.Hour))
	agreeOnHead(nodes)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for range 2 {
		decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
		if err != nil || decision.Status != rpc.Verified {
			t.Fatalf("anchored read failed: %+v %v", decision, err)
		}
	}
	if got := anchorEstablishments(nodes); got != 3 {
		t.Fatalf("expected one fleet-wide establishment (3 queries), got %d", got)
	}

	balances := 0
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBalance {
				balances++
			}
		}
	}
	if balances != 6 {
		t.Fatalf("expected both reads to fan out balances, got %d", balances)
	}
}

// TestAnchorCachingReestablishesAfterTTL proves the cache bounds staleness:
// once the TTL lapses the next read establishes a fresh reference.
func TestAnchorCachingReestablishesAfterTTL(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithAnchorCaching(30*time.Millisecond))
	agreeOnHead(nodes)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest"); err != nil {
		t.Fatal(err)
	}
	if got := anchorEstablishments(nodes); got != 6 {
		t.Fatalf("expected a fresh establishment after TTL (6 queries), got %d", got)
	}
}

// TestGasPriceIsSelectedWithOutlierEvidence pins the Variable-group
// contract end to end: the fleet's lower median wins, unanimous fleets still
// stay Selected (never Verified), and outliers ride as evidence.
func TestGasPriceIsSelectedWithOutlierEvidence(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	nodes[0].Return("eth_gasPrice", "0x3b9aca00") // 1 gwei
	nodes[1].Return("eth_gasPrice", "0x3b9aca00")
	nodes[2].Return("eth_gasPrice", "0x77359400") // 2 gwei outlier
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, decision, err := client.GasPrice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.Int64() != 1_000_000_000 {
		t.Fatalf("expected median 1 gwei, got %v", value)
	}
	if decision.Status != rpc.Selected || len(decision.Support) != 2 || len(decision.Reject) != 1 {
		t.Fatalf("expected selected median with outlier evidence, got %+v", decision)
	}
}

// TestGasPriceUnanimousStaysSelected proves the honesty ceiling through the
// public API: unanimity on a moving value must not upgrade the class.
func TestGasPriceUnanimousStaysSelected(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return("eth_gasPrice", "0x3b9aca00")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	value, decision, err := client.GasPrice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("unanimous variable result must stay selected, got %v", decision.Status)
	}
	if len(decision.Support) != 3 || decision.Reject != nil {
		t.Fatalf("expected the whole fleet in support, got %+v", decision)
	}
	if value.Int64() <= 0 {
		t.Fatalf("expected a decoded positive gas price, got %v", value)
	}
}

// TestPeerCountIsUpstreamLocalSelected pins the Local-group contract: each
// answer describes one provider's own state, so the first healthy source's
// value stands as Selected — differing answers are not dissent.
func TestPeerCountIsUpstreamLocalSelected(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	for i, node := range nodes {
		node.Return("net_peerCount", fmt.Sprintf("0x%x", 5+i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	count, decision, err := client.PeerCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("expected the first source's local count, got %d", count)
	}
	if decision.Status != rpc.Selected || len(decision.Support) != 1 || len(decision.Reject) != 0 {
		t.Fatalf("expected a selected local read naming its one source, got %+v", decision)
	}
}

// TestSyncingReportsConsultedSourceState covers both wire shapes of
// eth_syncing through the typed wrapper.
func TestSyncingReportsConsultedSourceState(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return("eth_syncing", false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progress, decision, err := client.Syncing(ctx)
	if err != nil || progress != nil || decision.Status != rpc.Selected {
		t.Fatalf("expected synced-fleet nil progress, got %v %+v %v", progress, decision.Status, err)
	}

	nodes[0].Return("eth_syncing", map[string]any{
		"startingBlock": "0x0", "currentBlock": "0x64", "highestBlock": "0xc8",
	})
	progress, _, err = client.Syncing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress == nil || progress.CurrentBlock != 100 || progress.HighestBlock != 200 {
		t.Fatalf("expected decoded sync progress, got %+v", progress)
	}
}

// TestClientHandlesConcurrentCallsUnderSharedRecorder exercises the
// Recorder contract (safe for concurrent use) and the client's fan-out under
// -race, mixing plain and anchored reads through one sink.
func TestClientHandlesConcurrentCallsUnderSharedRecorder(t *testing.T) {
	pm := dprom.New()
	client, nodes := fleet(t, 1, 1, 3, ethguard.WithRecorder(pm))
	agreeOnHead(nodes)

	const workers = 6
	var wg sync.WaitGroup
	failed := make(chan error, 1)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 5 {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var err error
				if i%2 == 0 {
					_, _, err = client.ChainID(ctx)
				} else {
					_, err = client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
				}
				cancel()
				if err != nil {
					select {
					case failed <- err:
					default:
					}
					return
				}
			}
		}(i)
	}
	wg.Wait()
	select {
	case err := <-failed:
		t.Fatal(err)
	default:
	}
}

// receiptFixture builds a schema-valid receipt as real providers emit it:
// hexadecimal quantities as strings, from/to present, contractAddress null
// for a non-creation transaction, and a zero bloom because the logs are
// empty (the derivation rule DecodeReceipt checks holds by construction).
func receiptFixture(txHash string) map[string]any {
	return map[string]any{
		"transactionHash":   txHash,
		"transactionIndex":  "0x1",
		"blockHash":         "0x" + strings.Repeat("11", 32),
		"blockNumber":       "0x10",
		"from":              "0x" + strings.Repeat("aa", 20),
		"to":                "0x" + strings.Repeat("bb", 20),
		"cumulativeGasUsed": "0x5208",
		"gasUsed":           "0x5208",
		"contractAddress":   nil,
		"logs":              []any{},
		"logsBloom":         "0x" + strings.Repeat("00", 256),
		"type":              "0x0",
		"status":            "0x1",
		"effectiveGasPrice": "0x4a817c800",
	}
}

func TestClientVerifiesReceiptAcrossSources(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	txHash := "0x" + strings.Repeat("42", 32)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, receiptFixture(txHash))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipt, decision, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TxHash != common.HexToHash(txHash) || decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected verified receipt across three sources, got %+v %+v", receipt, decision)
	}

	// The wire must carry the exact requested hash to every source.
	for i, node := range nodes {
		var saw bool
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetTransactionReceipt {
				continue
			}
			var got string
			if err := json.Unmarshal(call.Params[0], &got); err != nil || got != txHash {
				t.Fatalf("node %d was asked for %s, want %s", i, call.Params[0], txHash)
			}
			saw = true
		}
		if !saw {
			t.Fatalf("node %d was never asked for the receipt", i)
		}
	}
}

func TestClientRejectsReceiptThatDoesNotMatchRequestedTransaction(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	returnedHash := "0x" + strings.Repeat("42", 32)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, receiptFixture(returnedHash))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("43", 32))
	if _, _, err := client.TransactionReceipt(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested receipt, got %v", err)
	}
}

func TestClientVerifiesReceiptAbsenceAsNotFound(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, decision, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("42", 32)))
	if !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("expected ethereum.NotFound, got %v", err)
	}
	if decision.Status != rpc.Verified {
		t.Fatalf("expected unanimous absence to verify like any answer, got %+v", decision)
	}
}

func TestClientFailsClosedWhenReceiptPresenceSplits(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	receipt := receiptFixture("0x" + strings.Repeat("42", 32))
	for _, node := range nodes[:2] {
		node.Return(methods.MethodGetTransactionReceipt, receipt)
	}
	nodes[2].Return(methods.MethodGetTransactionReceipt, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, decision, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("42", 32)))
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("expected conflict on split presence, got %v", err)
	}
	if len(decision.Support) != 2 || len(decision.Reject) != 1 {
		t.Fatalf("expected 2 support and 1 reject, got %+v", decision)
	}
}

func TestClientRejectsUnanimousReceiptMissingRequiredFields(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	broken := receiptFixture("0x" + strings.Repeat("42", 32))
	delete(broken, "logsBloom")
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, broken)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Normalization compares shapes, not schemas, so the fleet agrees; the
	// required-field set binds one stage later, at the typed decode boundary.
	_, decision, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("42", 32)))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for schema-invalid unanimous payload, got %v", err)
	}
	if decision.Status != rpc.Verified {
		t.Fatalf("expected verification to pass before the typed boundary, got %+v", decision)
	}
}

// signedTxFixture marshals a signed legacy transaction the way a node
// reports it: the envelope fields plus the context fields (hash, from, and
// blockNumber when mined) that ride alongside them on the wire.
func signedTxFixture(t *testing.T, mined bool) json.RawMessage {
	t.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x" + strings.Repeat("bb", 20))
	tx := types.NewTransaction(7, to, big.NewInt(1_000_000_000_000_000_000), 21000, big.NewInt(1_000_000_000), nil)
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", signed.Hash().Hex()))
	obj["from"] = json.RawMessage(fmt.Sprintf("%q", crypto.PubkeyToAddress(key.PublicKey).Hex()))
	if mined {
		obj["blockNumber"] = json.RawMessage(`"0x10"`)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClientVerifiesTransactionByHash(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	raw := signedTxFixture(t, true)
	var want types.Transaction
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, json.RawMessage(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, decision, err := client.TransactionByHash(ctx, want.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if tx.Hash() != want.Hash() || decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected verified transaction across three sources, got %+v %+v", tx, decision)
	}
}

func TestClientRejectsTransactionThatDoesNotMatchRequestedHash(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	raw := signedTxFixture(t, true)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, json.RawMessage(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("42", 32))
	if _, _, err := client.TransactionByHash(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested transaction, got %v", err)
	}
}

func TestClientRejectsTransactionWithFabricatedHash(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	broken := signedTxFixture(t, true)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(broken, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", "0x"+strings.Repeat("77", 32)))
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, json.RawMessage(out))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The fleet agrees, so verification passes; the wire hash disagrees with
	// the recomputed signature hash, so the typed boundary rejects it.
	if _, _, err := client.TransactionByHash(ctx, common.HexToHash("0x"+strings.Repeat("42", 32))); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for fabricated wire hash, got %v", err)
	}
}

func TestClientMapsTransactionAbsenceToNotFound(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, decision, err := client.TransactionByHash(ctx, common.HexToHash("0x"+strings.Repeat("42", 32)))
	if !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("expected ethereum.NotFound, got %v", err)
	}
	if decision.Status != rpc.Verified {
		t.Fatalf("expected unanimous absence to verify like any answer, got %+v", decision)
	}
}

func TestClientPinsBlockReceiptsToVerifiedReference(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	receipts := []any{receiptFixture("0x" + strings.Repeat("42", 32))}
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
		node.Handle(methods.MethodGetBlockReceipts, func(c rpctest.Call) (any, error) {
			var ref string
			json.Unmarshal(c.Params[0], &ref)
			if ref == "0xabc" {
				return receipts, nil
			}
			return fmt.Sprintf("unpinned reference %s", ref), nil // divergence for anything unrewritten
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodGetBlockReceipts, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected anchored receipts read to verify across three sources, got %+v", decision)
	}
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBlockReceipts {
				continue
			}
			var ref string
			if err := json.Unmarshal(call.Params[0], &ref); err != nil || ref == "latest" || ref == "safe" || ref == "finalized" {
				t.Fatalf("node %d received unrewritten reference %s", i, call.Params[0])
			}
		}
	}
}

func TestClientMapsUnknownBlockReceiptsToNotFound(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := client.BlockReceipts(ctx, common.HexToHash("0x"+strings.Repeat("11", 32))); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("expected ethereum.NotFound, got %v", err)
	}
}

func TestClientRejectsBlockReceiptsFromAnotherBlock(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []any{
			receiptFixture("0x" + strings.Repeat("42", 32)),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("22", 32))
	if _, _, err := client.BlockReceipts(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for receipts from another block, got %v", err)
	}
}

func TestMaxPriorityFeePerGasTakesLowerMedian(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	nodes[0].Return(methods.MethodMaxPriorityFeePerGas, "0x3b9aca00") // 1 gwei
	nodes[1].Return(methods.MethodMaxPriorityFeePerGas, "0x3b9aca00")
	nodes[2].Return(methods.MethodMaxPriorityFeePerGas, "0x77359400") // 2 gwei outlier
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tip, decision, err := client.MaxPriorityFeePerGas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Int64() != 1_000_000_000 {
		t.Fatalf("expected lower median 1 gwei, got %v", tip)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("expected Selected ceiling for a variable result, got %+v", decision)
	}
}

func TestClientVerifiesNetVersionAcrossSpellings(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	nodes[0].Return(methods.MethodNetVersion, "1")
	nodes[1].Return(methods.MethodNetVersion, "1")
	nodes[2].Return(methods.MethodNetVersion, "01") // same identity, looser spelling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	version, decision, err := client.NetVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version.Int64() != 1 || decision.Status != rpc.Verified || len(decision.Support) != 3 {
		t.Fatalf("expected cross-spelling net_version agreement to verify, got %v %+v", version, decision)
	}
}

func TestListeningIsUpstreamLocalSelected(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	nodes[0].Return(methods.MethodListening, true)
	nodes[1].Return(methods.MethodListening, false) // differing answers are not dissent
	nodes[2].Return(methods.MethodListening, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listening, decision, err := client.Listening(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !listening {
		t.Fatalf("expected the first source's answer, got %v", listening)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("expected Selected ceiling for a local result, got %+v", decision)
	}
}

func TestClientBlockNumberDerivesFromVerifiedReference(t *testing.T) {
	t.Run("agrees on one head", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		// A schema-valid block: the derivation decodes the reference fully,
		// not just its head identity. Anchor establishment rides the head
		// policy; the height fetch rides the hash-addressed method.
		pinned := fixtureBlock(t, 16)
		for _, node := range nodes {
			node.Return("eth_getBlockByNumber", json.RawMessage(pinned))
			node.Return("eth_getBlockByHash", json.RawMessage(pinned))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		height, err := client.BlockNumber(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if height != 16 {
			t.Fatalf("expected derived height 16, got %d", height)
		}
		// The derivation must be visible in what does NOT hit the wire:
		// eth_blockNumber is never fanned out to any source.
		for i, node := range nodes {
			for _, call := range node.Calls() {
				if call.Method == "eth_blockNumber" {
					t.Fatalf("node %d received an eth_blockNumber fan-out", i)
				}
			}
		}
	})

	t.Run("rejects a fetched block that does not match the anchor", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		anchored := fixtureBlock(t, 16)
		wrong := fixtureBlock(t, 17)
		for _, node := range nodes {
			node.Return(methods.MethodGetBlockByNumber, json.RawMessage(anchored))
			node.Return(methods.MethodGetBlockByHash, json.RawMessage(wrong))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := client.BlockNumber(ctx); !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("expected ErrMalformedResponse for block outside the anchor, got %v", err)
		}
	})

	t.Run("divergent tips fail closed through anchor establishment", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 3)
		nodes[0].Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
		nodes[1].Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
		nodes[2].Return("eth_getBlockByNumber", map[string]any{"number": "0xf", "hash": "0xdifferent"})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := client.BlockNumber(ctx); !errors.Is(err, rpc.ErrConflict) {
			t.Fatalf("expected anchor failure on divergent tips, got %v", err)
		}
	})
}

// fixtureBlock marshals a self-consistent block at the given height: the
// wire hash equals the header's computed hash, so DecodeBlock's integrity
// check passes.
func fixtureBlock(t *testing.T, number int64) string {
	t.Helper()
	head := &types.Header{
		ParentHash:  common.HexToHash("0x" + strings.Repeat("01", 32)),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x" + strings.Repeat("02", 32)),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x" + strings.Repeat("03", 32)),
		Difficulty:  new(big.Int), // post-merge zero; must survive the round-trip
		Number:      big.NewInt(number),
		GasLimit:    30_000_000,
		Time:        5,
	}
	block := types.NewBlockWithHeader(head)
	raw, err := head.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", block.Hash().Hex()))
	obj["transactions"] = json.RawMessage(`[]`)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// balanceDissentAt scripts three sources to answer want at the pinned hash
// and the rest dissent, so the winning cluster holds exactly three of the
// five attempted sources.
func balanceDissentAt(nodes []*rpctest.Node, hash, want, dissent string) {
	for i, node := range nodes {
		agree := i < 3
		node.Handle("eth_getBalance", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == hash {
				if agree {
					return want, nil
				}
				return dissent, nil
			}
			return "0x1", nil // divergent elsewhere; only the anchor matters
		})
	}
}

// TestClientQuorumCoversAnchoredStateReads pins decision 0006's reach: the
// client-level quorum option must reach the anchored state reads, not only
// the plainly strict methods. Three of five sources agree at the verified
// anchor while two dissent — a conflict under the default full-agreement
// mode, a Verified majority cluster under WithQuorum(3).
func TestClientQuorumCoversAnchoredStateReads(t *testing.T) {
	t.Run("quorum resolves the anchored majority cluster", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 5, ethguard.WithQuorum(3))
		agreeOnHead(nodes) // anchor establishment still sees full agreement
		balanceDissentAt(nodes, "0xabc", "0x64", "0x65")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
		if err != nil {
			t.Fatalf("quorum should resolve the anchored read over dissent: %v", err)
		}
		if decision.Status != rpc.Verified || len(decision.Support) != 3 {
			t.Fatalf("expected a verified 3-source cluster, got %+v", decision)
		}
		if string(decision.Value) != `"0x64"` {
			t.Fatalf("expected the majority cluster's balance, got %s", decision.Value)
		}
	})

	t.Run("without quorum the same dissent conflicts", func(t *testing.T) {
		client, nodes := fleet(t, 1, 1, 5)
		agreeOnHead(nodes)
		balanceDissentAt(nodes, "0xabc", "0x64", "0x65")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
		if !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("expected full-agreement mode to conflict, got %v", err)
		}
	})
}

// callAt scripts eth_call to answer want only at the anchored hash and
// revert everywhere else, so the test proves which reference rode the wire.
func callAt(nodes []*rpctest.Node, hash, want string) {
	for _, node := range nodes {
		node.Handle("eth_call", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == hash {
				return want, nil
			}
			return nil, rpctest.RPCError(-32000, "execution reverted")
		})
	}
}

func TestClientVerifiesCallOutcomeAcrossSources(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes)
	callAt(nodes, "0xabc", "0xdeadbeef")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodCall, map[string]any{"to": "0x1", "input": "0x00"}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Verified || string(decision.Value) != `"0xdeadbeef"` {
		t.Fatalf("expected verified call result, got %+v", decision)
	}
	if decision.BlockRef != "0xabc" {
		t.Fatalf("expected the anchored rewrite recorded as block ref, got %q", decision.BlockRef)
	}
}

func TestClientVerifiedRevertIsAnObservation(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes)
	for _, node := range nodes {
		node.Fail("eth_call", -32000, "execution reverted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodCall, map[string]any{"to": "0x1", "input": "0x00"}, "latest")
	if err != nil {
		t.Fatalf("a unanimous revert is a verified observation, not a failure: %v", err)
	}
	if decision.Status != rpc.Verified {
		t.Fatalf("expected verified revert observation, got %+v", decision)
	}
	if !strings.Contains(string(decision.Value), "execution reverted") {
		t.Fatalf("expected the revert envelope as value, got %s", decision.Value)
	}
}

func TestClientFailsClosedWhenCallSplitsAcrossClasses(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes)
	callAt(nodes, "0xabc", "0xdeadbeef") // two sources answer; the third reverts below
	nodes[2].Handle("eth_call", func(c rpctest.Call) (any, error) {
		return nil, rpctest.RPCError(-32000, "execution reverted")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Call(ctx, methods.MethodCall, map[string]any{"to": "0x1", "input": "0x00"}, "latest")
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("expected a result/revert split to conflict, got %v", err)
	}
}

func TestEstimateGasIsAnchoredMedian(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes)
	estimates := []string{"0x64", "0x66", "0x68"}
	for i, node := range nodes {
		want := estimates[i]
		node.Handle("eth_estimateGas", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == "0xabc" {
				return want, nil
			}
			return nil, rpctest.RPCError(-32000, "execution reverted")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodEstimateGas, map[string]any{"to": "0x1", "gas": 21000}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected || string(decision.Value) != `"0x66"` {
		t.Fatalf("expected selected lower median 0x66, got %+v", decision)
	}
}

func TestGetLogsPinsTheFilterRangeOnTheWire(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes)
	logs := []map[string]any{{"address": "0xaddr", "blockNumber": "0x10", "logIndex": "0x0"}}
	for _, node := range nodes {
		node.Handle("eth_getLogs", func(c rpctest.Call) (any, error) {
			if len(c.Params) != 1 {
				return nil, rpctest.RPCError(-32602, "expected filter object")
			}
			var filter map[string]any
			if err := json.Unmarshal(c.Params[0], &filter); err != nil {
				return nil, rpctest.RPCError(-32602, "malformed filter")
			}
			if filter["fromBlock"] == "0xabc" && filter["toBlock"] == "0xabc" {
				return logs, nil
			}
			return nil, rpctest.RPCError(-32602, "range was not pinned to the anchor")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodGetLogs, map[string]any{"fromBlock": "latest", "toBlock": "finalized"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Verified || decision.BlockRef != "0xabc" {
		t.Fatalf("expected verified pinned log query, got %+v", decision)
	}
}

func TestFeeHistoryPinsNewestToTheVerifiedHeight(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes) // head fixture: number 0x10, hash 0xabc
	fixture := map[string]any{
		"oldestBlock":   "0xf",
		"baseFeePerGas": []string{"0x1", "0x2", "0x3"},
		"gasUsedRatio":  []float64{0.5, 0.6, 0.7},
	}
	for _, node := range nodes {
		node.Handle("eth_feeHistory", func(c rpctest.Call) (any, error) {
			var newest string
			if len(c.Params) == 3 && json.Unmarshal(c.Params[1], &newest) == nil && newest == "0x10" {
				return fixture, nil
			}
			return nil, rpctest.RPCError(-32602, "newest was not the verified height")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	history, decision, err := client.FeeHistory(ctx, 3, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Verified || decision.BlockRef != "0x10" {
		t.Fatalf("expected verified height-pinned history, got %+v", decision)
	}
	if history.OldestBlock.Uint64() != 15 || len(history.BaseFeePerGas) != 3 {
		t.Fatalf("unexpected decoded history: %+v", history)
	}
}

// hashOnlyAnchor is a custom resolver without the height capability.
type hashOnlyAnchor struct{}

func (hashOnlyAnchor) AnchorBlockHash(context.Context) (string, error) { return "0xabc", nil }

// TestCachedAnchorRejectsHeightReadsWithoutHeightCapability pins the audit
// fix: a cached resolver whose inner cannot produce heights must fail
// height-anchored reads with a clear error, never silently pin them to
// height zero.
func TestCachedAnchorRejectsHeightReadsWithoutHeightCapability(t *testing.T) {
	client, _ := fleet(t, 1, 1, 3,
		ethguard.WithAnchorResolver(hashOnlyAnchor{}),
		ethguard.WithAnchorCaching(time.Minute))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := client.FeeHistory(ctx, 2, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "AnchorHeightProvider") {
		t.Fatalf("expected a clear height-capability error, got %v", err)
	}
}

// TestConfigPresetSpellingSelectsTheSameStrategy pins decision 0004's
// normalization: "STRICT" passes validation and must select the strict
// strategy, not silently flip to the finalized preset. Divergent latest
// heads conflict under strict anchoring and would succeed under finalized.
func TestConfigPresetSpellingSelectsTheSameStrategy(t *testing.T) {
	cfg := config.Config{ChainID: 1, Anchoring: &config.AnchoringConfig{Preset: "STRICT"}}
	nodes := make([]*rpctest.Node, 0, 3)
	for i := range 3 {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		nodes = append(nodes, node)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%c", 'a'+i),
			Endpoint: node.URL(),
		})
	}
	scriptFinalizedHeads(nodes,
		func(i int) map[string]any { // divergent latest heads
			return map[string]any{"number": fmt.Sprintf("0x%x", 16-i), "hash": fmt.Sprintf("0xhead%d", i)}
		},
		func(int) map[string]any { // one common finalized head
			return map[string]any{"number": "0x8", "hash": "0xdead"}
		},
	)
	client, err := ethguard.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("expected strict anchoring to fail closed on divergent heads, got %v", err)
	}
}

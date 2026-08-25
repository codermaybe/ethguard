package ethguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

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

// agreeOnHead scripts every node into one common block head, with balances
// that agree only at the pinned hash and diverge for any moving tag.
func agreeOnHead(nodes []*rpctest.Node) {
	head := map[string]any{"number": "0x10", "hash": "0xabc"}
	for i, node := range nodes {
		node.Return("eth_getBlockByNumber", head)
		node.Handle("eth_getBalance", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == "0xabc" {
				return "0x64", nil // agreed balance at the anchored block
			}
			return fmt.Sprintf("0x%x", 100+i), nil // divergent answers for a moving tag
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

func TestClientClassifiesFailuresThroughPublicSentinels(t *testing.T) {
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

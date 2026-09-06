package pool

import (
	"context"
	"testing"
	"time"
)

// newTipPool joins three sources with per-source block heights (via dialer
// offsets) and enables tip sampling with the given lag bound.
func newTipPool(t *testing.T, maxLag uint64, heights ...int64) (*Pool, *newFakeDialer, []*Source) {
	t.Helper()
	names := []string{"a", "b", "c"}
	d := &newFakeDialer{
		dials:   make(map[string]int),
		offsets: make(map[string]int64),
		nodes:   make(map[string]*fakeNode),
	}
	for i, name := range names {
		d.offsets["https://"+name+".example"] = heights[i]
	}
	p := newPoolWithDialerAndOptions(d, 0, WithTipTracking(time.Hour, maxLag))
	t.Cleanup(p.Close)
	for _, name := range names {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}
	return p, d, p.Sources()
}

func TestTipSamplingRecordsHeights(t *testing.T) {
	p, _, sources := newTipPool(t, 32, 100, 101, 102)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Warm the sources: probes observe established connections only, so
	// first contact stays caller-driven.
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatal(err)
	}
	p.RefreshTips(ctx)

	for i, src := range sources {
		height, ok := src.tipHeight()
		if !ok || height != uint64(102+i) {
			t.Fatalf("source %d: expected sampled height %d, got %d (ok=%v)", i, 102+i, height, ok)
		}
		if got := src.Health().TipHeight; got != uint64(102+i) {
			t.Fatalf("source %d: snapshot tip = %d", i, got)
		}
	}
}

// TestProbeNeverDialsColdSources locks in the lazy-dialing boundary: tip
// probes observe established connections, they do not create them.
func TestProbeNeverDialsColdSources(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndOptions(d, 0, WithTipTracking(time.Hour, 32))
	defer p.Close()
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}

	p.RefreshTips(context.Background())
	if got := d.dials["https://a.example"]; got != 0 {
		t.Fatalf("a probe dialed a cold source %d time(s)", got)
	}
}

func TestRefreshTipsIsANoOpWhenDisabled(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	defer p.Close()
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	p.RefreshTips(context.Background()) // must not panic or block
}

func TestFreshnessGateExcludesTrailingSource(t *testing.T) {
	// Heights 100/100/10: the lower median is 100, so with a bound of 16
	// the trailing source falls out of fan-outs but keeps being probed.
	p, d, sources := newTipPool(t, 16, 100, 100, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatal(err)
	}
	p.RefreshTips(ctx)

	emissions, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(emissions) != 2 {
		t.Fatalf("expected the trailing source to be gated out, got %+v", emissions)
	}
	for _, em := range emissions {
		if em.Src == sources[2] {
			t.Fatal("gated source received traffic")
		}
	}

	// Recovery: once the source catches up, the next probe readmits it.
	d.setOffset("https://c.example", 104)
	p.RefreshTips(ctx)

	emissions, err = p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect after recovery: %v", err)
	}
	if len(emissions) != 3 {
		t.Fatalf("expected recovered source to be readmitted, got %+v", emissions)
	}
}

func TestFreshnessGateResistsHeightLies(t *testing.T) {
	// One provider claims an absurd height; the lower median reference must
	// keep it from voting honest sources out of the fan-out.
	p, _, _ := newTipPool(t, 8, 100, 101, 1_000_000)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatal(err)
	}
	p.RefreshTips(ctx)

	emissions, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(emissions) != 3 {
		t.Fatalf("a lone height lie must not gate honest sources, got %+v", emissions)
	}
}

func TestFreshnessGateStaysOffUntilSampled(t *testing.T) {
	p, _, _ := newTipPool(t, 8, 5, 5, 500)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	emissions, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(emissions) != 3 {
		t.Fatalf("unsampled tips must never gate traffic, got %+v", emissions)
	}
}

// TestProbeDetectsRecoveryThroughOpenCircuit proves proactive recovery: a
// source whose failure circuit is closed to user traffic still receives
// probes, and one successful probe readmits it immediately.
func TestProbeDetectsRecoveryThroughOpenCircuit(t *testing.T) {
	p, _, _ := newTipPool(t, 32, 50, 50, 50)
	sources := p.Sources()
	target := sources[0]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Trip the consecutive-failure circuit on one source.
	for i := 0; i < eligibleFailureThreshold; i++ {
		if _, err := p.Collect(ctx, "eth_protocolFailure", nil, CollectOptions{}); err != nil {
			t.Fatalf("failure %d should ride as faults, got %v", i+1, err)
		}
	}
	if target.eligible(time.Now()) {
		t.Fatal("expected the circuit to hold the source out of user fan-outs")
	}

	// The probe ignores cooldowns; its success resets the circuit.
	p.RefreshTips(ctx)
	if !target.eligible(time.Now()) {
		t.Fatal("expected a successful probe to readmit the source")
	}
	if got := target.Health().ConsecutiveFailures; got != 0 {
		t.Fatalf("expected failures reset by probe success, got %d", got)
	}
}

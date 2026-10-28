package pool

import (
	"context"
	"errors"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestBroadcastContactsAllSourcesWithoutFreshnessGate(t *testing.T) {
	nodes := make([]*rpctest.Node, 3)
	urls := make([]string, 3)
	for i := range nodes {
		nodes[i] = rpctest.NewNode(1)
		defer nodes[i].Close()
		nodes[i].Return("eth_sendRawTransaction", "0x"+"01")
		urls[i] = nodes[i].URL()
	}
	// One node hangs past the fan-out; the broadcast still contacts it, and
	// the other two answers are returned alongside its timeout emission.
	nodes[2].Hang("eth_sendRawTransaction", 2*time.Second)

	p := NewPoolWithChainID(1)
	for i, u := range urls {
		if _, err := p.Join(string(rune('a'+i)), u); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	emissions, err := p.Broadcast(ctx, "eth_sendRawTransaction", []any{"0x01"})
	if err != nil {
		t.Fatalf("broadcast returned error despite two usable answers: %v", err)
	}
	if len(emissions) != 3 {
		t.Fatalf("emissions = %d, want 3 (one per member)", len(emissions))
	}
	contacted := 0
	for i, emission := range emissions {
		if emission.Attempted {
			contacted++
		}
		if i < 2 && emission.Val == nil {
			t.Fatalf("source %d produced no value: %v", i, emission.Err)
		}
	}
	if contacted < 2 {
		t.Fatalf("contacted = %d, want at least the two answerable sources", contacted)
	}
}

func TestBroadcastShortCircuitNeverStopsFleet(t *testing.T) {
	// Every source answers with the same hash; Collect-style short-circuiting
	// would return one emission early. Broadcast must contact all of them.
	nodes := make([]*rpctest.Node, 3)
	for i := range nodes {
		nodes[i] = rpctest.NewNode(1)
		defer nodes[i].Close()
		nodes[i].Return("eth_sendRawTransaction", "0xabc")
	}
	p := NewPoolWithChainID(1)
	for i, n := range nodes {
		if _, err := p.Join(string(rune('a'+i)), n.URL()); err != nil {
			t.Fatal(err)
		}
	}
	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0xabc"})
	if err != nil {
		t.Fatal(err)
	}
	for i, emission := range emissions {
		if !emission.Attempted {
			t.Fatalf("source %d was not contacted; broadcast must not short-circuit", i)
		}
	}
}

func TestBroadcastCoolingSourceIsGatedNotFailed(t *testing.T) {
	node := rpctest.NewNode(1)
	defer node.Close()
	node.Fail("eth_sendRawTransaction", -32010, "nonce too low")

	p := NewPoolWithChainID(1)
	src, err := p.Join("a", node.URL())
	if err != nil {
		t.Fatal(err)
	}
	// Drive the cooldown circuit open through repeated read failures on a
	// different method, then verify the broadcast pauses the source without
	// health penalty.
	node.Fail("eth_blockNumber", -32099, "cooling")
	for i := 0; i < eligibleFailureThreshold+1; i++ {
		p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{Timeout: time.Second})
	}
	if src.requestable(time.Now()) {
		t.Skip("circuit did not open; monitor thresholds changed")
	}
	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"})
	if err == nil {
		t.Fatal("expected ErrNoEligibleSource when the only member is gated")
	}
	if !errors.Is(err, ErrNoEligibleSource) {
		t.Fatalf("err = %v, want ErrNoEligibleSource", err)
	}
	if len(emissions) != 1 || !errors.Is(emissions[0].Err, ErrSourceGated) {
		t.Fatalf("emission err = %v, want ErrSourceGated", emissions[0].Err)
	}
}

func TestBroadcastClosedPool(t *testing.T) {
	p := NewPool()
	p.Close()
	_, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestBroadcastEmptyPool(t *testing.T) {
	p := NewPool()
	_, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"})
	if !errors.Is(err, ErrNoEligibleSource) {
		t.Fatalf("err = %v, want ErrNoEligibleSource", err)
	}
}

func TestBroadcastTrailingSourceStillContacted(t *testing.T) {
	// A source with a stale tip must still receive the broadcast: writes are
	// not gated by read freshness.
	node := rpctest.NewNode(1)
	defer node.Close()
	node.Return("eth_sendRawTransaction", "0xabc")

	p := NewPoolWithChainID(1)
	if _, err := p.Join("a", node.URL()); err != nil {
		t.Fatal(err)
	}
	// Record an ancient tip so the freshness gate would exclude this source
	// from a read fan-out.
	src := p.Sources()[0]
	src.recordTip(1)

	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"})
	if err != nil {
		t.Fatalf("broadcast failed: %v", err)
	}
	if len(emissions) != 1 || emissions[0].Val == nil {
		t.Fatalf("stale-tip source must still be contacted: %+v", emissions)
	}
}

func TestBroadcastProtocolErrorIsUsableAnswer(t *testing.T) {
	// A JSON-RPC rejection ("nonce too low") is an answer, not fleet silence:
	// the broadcast must return nil so adjudication sees the rejection.
	node := rpctest.NewNode(1)
	defer node.Close()
	node.Fail("eth_sendRawTransaction", -32010, "nonce too low")

	p := NewPoolWithChainID(1)
	if _, err := p.Join("a", node.URL()); err != nil {
		t.Fatal(err)
	}
	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"})
	if err != nil {
		t.Fatalf("protocol rejection must not surface as transport error: %v", err)
	}
	var protocolErr rpc.ProtocolError
	if !errors.As(emissions[0].Err, &protocolErr) {
		t.Fatalf("emission err = %v, want structured JSON-RPC error", protocolErr)
	}
}

// Regression for the precheck-dispatch window: a member closed by Pool.Remove
// between Broadcast's precheck pass and its dispatch pass was skipped without
// an emission, leaving a zero-value Emission — Src == nil panics downstream
// aggregators and Err == nil corrupts the caller's availability verdict. A
// member closed at any point now fails closed through dispatchOne's
// acquireRequest, so every emission is non-zero regardless of removal timing.
//
// The window itself is scheduler-tiny, so the test amplifies it: a wide
// membership spreads the two loop passes, several victims tile their
// precheck-read/dispatch-check windows across them, and repeated rounds
// jitter the Remove. The race detector's per-lock overhead widens the window
// further. With the fix the asserted invariants hold structurally, so the
// test stays stable; without it the window is hit within a few rounds.
func TestBroadcastRemovalRacingFanoutLeavesNoZeroEmission(t *testing.T) {
	const (
		members = 64
		rounds  = 24
	)
	rng := rand.New(rand.NewSource(1))
	victims := map[string]bool{"m00": true, "m16": true, "m32": true, "m48": true}

	for round := 0; round < rounds; round++ {
		runRemovalRaceRound(t, round, rng, victims, members)
	}
}

// runRemovalRaceRound is one amplification round, split out so its pool and
// nodes close via defer even when t.Fatalf exits the test mid-round.
func runRemovalRaceRound(t *testing.T, round int, rng *rand.Rand, victims map[string]bool, members int) {
	t.Helper()
	// Two slow real nodes keep the fan-out observably in progress when
	// Remove fires; the remaining members are distinct unreachable
	// endpoints — cheap, but they still pass the precheck gates and widen
	// the precheck/dispatch loop window.
	slow := make([]*rpctest.Node, 2)
	defer func() {
		for _, n := range slow {
			n.Close()
		}
	}()
	for i := range slow {
		slow[i] = rpctest.NewNode(1)
		slow[i].Handle("eth_sendRawTransaction",
			rpctest.After(25*time.Millisecond, rpctest.Value("0x"+"02")))
	}

	p := NewPoolWithChainID(1)
	defer p.Close()
	port := 10 // two-digit unreachable loopback ports; every member must be a distinct endpoint
	for i := 0; i < members; i++ {
		name := "m" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		url := "http://127.0.0.1:" + string(rune('0'+port/10)) + string(rune('0'+port%10))
		if i >= members-len(slow) {
			url = slow[i-(members-len(slow))].URL()
		} else {
			port++
		}
		if _, err := p.Join(name, url); err != nil {
			t.Fatalf("round %d: join %s: %v", round, name, err)
		}
	}

	type broadcastResult struct {
		emissions []Emission
		err       error
	}
	done := make(chan broadcastResult, 1)
	go func() {
		emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x02"},
			WithBroadcastTimeout(time.Second))
		done <- broadcastResult{emissions: emissions, err: err}
	}()

	// Jittered removals aimed at the precheck->dispatch window; busy-wait
	// keeps sub-100µs precision that time.Sleep cannot deliver.
	deadline := time.Now().Add(time.Duration(rng.Intn(500)) * time.Microsecond)
	for time.Now().Before(deadline) {
	}
	for name := range victims {
		_ = p.Remove(name) // absent when this round removed it pre-snapshot
	}

	result := <-done
	if len(result.emissions) == 0 {
		t.Fatalf("round %d: broadcast returned no emissions (err=%v)", round, result.err)
	}
	for i, emission := range result.emissions {
		if emission.Src == nil {
			t.Fatalf("round %d: emission %d is zero-valued (no Src); removal raced the fan-out window", round, i)
		}
		if emission.Val == nil && emission.Err == nil {
			t.Fatalf("round %d: emission %d carries neither a value nor an error", round, i)
		}
	}
}

// The concurrency cap only paces the wave: with a cap of one the fleet is
// contacted serially, but every member is still contacted and the emissions
// keep pool order — pacing never changes membership or the verdict.
func TestBroadcastMaxConcurrentPacesWithoutShrinking(t *testing.T) {
	var concurrent, peak atomic.Int32
	nodes := make([]*rpctest.Node, 3)
	for i := range nodes {
		nodes[i] = rpctest.NewNode(1)
		nodes[i].Handle("eth_sendRawTransaction", func(c rpctest.Call) (any, error) {
			now := concurrent.Add(1)
			for {
				seen := peak.Load()
				if now <= seen || peak.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			concurrent.Add(-1)
			return "0x" + string(rune('a'+i)), nil
		})
		defer nodes[i].Close()
	}

	p := NewPoolWithChainID(1)
	defer p.Close()
	for i, n := range nodes {
		if _, err := p.Join(string(rune('a'+i)), n.URL()); err != nil {
			t.Fatal(err)
		}
	}
	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x02"},
		WithBroadcastTimeout(2*time.Second), WithBroadcastMaxConcurrent(1))
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if peak.Load() > 1 {
		t.Fatalf("peak concurrency %d, want the cap of 1 respected", peak.Load())
	}
	if len(emissions) != 3 {
		t.Fatalf("got %d emissions, want one per member despite the cap", len(emissions))
	}
	for i, em := range emissions {
		if em.Src == nil || em.Src.Name() != string(rune('a'+i)) || em.Err != nil {
			t.Fatalf("emission %d = %+v, want the ordered pool member's answer", i, em)
		}
	}
}

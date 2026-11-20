package pool

import (
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"

	"context"
	"errors"
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

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	for i, u := range urls {
		if _, err := p.join(string(rune('a'+i)), u); err != nil {
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
	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	for i, n := range nodes {
		if _, err := p.join(string(rune('a'+i)), n.URL()); err != nil {
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

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	src, err := p.join("a", node.URL())
	if err != nil {
		t.Fatal(err)
	}
	// Open the cooldown circuit deterministically. JSON-RPC business errors
	// are usable protocol responses and therefore must not be relied on to
	// mutate transport-health state.
	for range eligibleFailureThreshold {
		src.stats.observeFailure(errors.New("transport reset"))
	}
	if src.requestable(time.Now()) {
		t.Fatalf("circuit did not open after %d transport failures", eligibleFailureThreshold)
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
	p := NewPoolWithOptions(0, WithDialer(nativehttp.NewDialer()))
	p.Close()
	_, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestBroadcastEmptyPool(t *testing.T) {
	p := NewPoolWithOptions(0, WithDialer(nativehttp.NewDialer()))
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

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	if _, err := p.join("a", node.URL()); err != nil {
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

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	if _, err := p.join("a", node.URL()); err != nil {
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

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	defer p.Close()
	for i, n := range nodes {
		if _, err := p.join(string(rune('a'+i)), n.URL()); err != nil {
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

// A cooldown-gated member of a caller-supplied broadcast basis keeps its
// slot: the emission stays at the member's snapshot position with the
// structured gate error and no attempt, the rest of the basis is contacted
// in the same wave, and the gate never becomes a health observation. The
// caller's snapshot order is the returned order, so a paused member cannot
// shift the write denominator's shape.
func TestBroadcastFixedBasisKeepsGatedMemberSlot(t *testing.T) {
	cooling := rpctest.NewNode(1)
	defer cooling.Close()
	healthy := rpctest.NewNode(1)
	defer healthy.Close()
	healthy.Return("eth_sendRawTransaction", "0xabc")

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	defer p.Close()
	gatedSrc, err := p.join("cooling", cooling.URL())
	if err != nil {
		t.Fatal(err)
	}
	liveSrc, err := p.join("healthy", healthy.URL())
	if err != nil {
		t.Fatal(err)
	}
	for range eligibleFailureThreshold {
		gatedSrc.stats.observeFailure(errors.New("transport reset"))
	}
	if gatedSrc.requestable(time.Now()) {
		t.Fatal("expected the cooldown circuit to open")
	}
	failuresBefore := gatedSrc.Health().Failures

	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"},
		WithBroadcastSources([]*Source{gatedSrc, liveSrc}))
	if err != nil {
		t.Fatalf("broadcast with a gated member must still deliver the healthy answer: %v", err)
	}
	if len(emissions) != 2 {
		t.Fatalf("emissions = %d, want one slot per snapshot member", len(emissions))
	}
	if emissions[0].Src != gatedSrc || emissions[1].Src != liveSrc {
		t.Fatalf("emission order = [%v, %v], want the snapshot order", emissions[0].Src.Name(), emissions[1].Src.Name())
	}
	gated := emissions[0]
	if gated.Attempted || !errors.Is(gated.Err, ErrSourceGated) {
		t.Fatalf("gated emission = %+v, want a structured pause with no attempt", gated)
	}
	var gate *SourceGateError
	if !errors.As(gated.Err, &gate) || gate.Reason != GateReasonCooldown {
		t.Fatalf("gate error = %v, want a cooldown gate diagnosis", gated.Err)
	}
	if emissions[1].Val == nil || !emissions[1].Attempted {
		t.Fatalf("healthy member emission = %+v, want an attempted answer", emissions[1])
	}
	if got := gatedSrc.Health().Failures; got != failuresBefore {
		t.Fatalf("gating changed health accounting: %d -> %d", failuresBefore, got)
	}
}

// A member the caller captured and that has since closed stays visible in
// the returned broadcast emissions as a failed slot, not silently dropped
// from the write denominator: the snapshot basis cannot shrink after a
// concurrent close. The live member is still contacted and its answer rides
// alongside the closed member's ErrClosed emission.
func TestBroadcastClosedMemberStaysVisibleInSnapshotBasis(t *testing.T) {
	healthy := rpctest.NewNode(1)
	defer healthy.Close()
	healthy.Return("eth_sendRawTransaction", "0xabc")

	p := NewPoolWithOptions(1, WithDialer(nativehttp.NewDialer()))
	defer p.Close()
	doomed, err := p.join("doomed", "https://doomed.example")
	if err != nil {
		t.Fatal(err)
	}
	liveSrc, err := p.join("live", healthy.URL())
	if err != nil {
		t.Fatal(err)
	}
	doomed.close()

	emissions, err := p.Broadcast(context.Background(), "eth_sendRawTransaction", []any{"0x01"},
		WithBroadcastSources([]*Source{doomed, liveSrc}))
	if err != nil {
		t.Fatalf("closed member must not fail the wave while a live answer exists: %v", err)
	}
	if len(emissions) != 2 {
		t.Fatalf("emissions = %d, want the closed member to keep its slot", len(emissions))
	}
	if emissions[0].Src != doomed || !errors.Is(emissions[0].Err, ErrClosed) || emissions[0].Attempted {
		t.Fatalf("closed member emission = %+v, want ErrClosed with no attempt", emissions[0])
	}
	if emissions[1].Val == nil || !emissions[1].Attempted {
		t.Fatalf("live member emission = %+v, want an attempted answer", emissions[1])
	}
}

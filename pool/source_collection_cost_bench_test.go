package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	upstream "github.com/codermaybe/ethguard/internal/transport"
)

// The source-collection cost baselines measure the pool's own fan-out
// machinery — gate decisions, slot dispatch, emission recording, and health
// accounting — against an in-process transport, so the numbers describe pool
// behavior rather than a network stack. The per-source adaptive scheduler
// (monitor.acquireRequest) paces sequential requests per source; every
// benchmark warms the pace down to its floor before timing, so the timed
// window sees the steady-state pacing cost production traffic sees.

const benchResultPayload = `{"jsonrpc":"2.0","id":1,"result":"0x1"}`

// benchFleetTransport answers one fixed result and counts the wire requests
// that actually crossed the transport seam. A non-nil hold point blocks each
// call until the channel is closed or the call's context fires, which lets a
// benchmark park requests deterministically.
type benchFleetTransport struct {
	requests atomic.Int64
	hold     atomic.Value // chan struct{} when set
	result   json.RawMessage
}

func (t *benchFleetTransport) CallContext(ctx context.Context, result any, _ string, _ ...any) error {
	t.requests.Add(1)
	if hold, ok := t.hold.Load().(chan struct{}); ok {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	out, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("bench fleet transport: result target is not *json.RawMessage")
	}
	*out = append((*out)[:0], t.result...)
	return nil
}

func (t *benchFleetTransport) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int), nil
}

func (t *benchFleetTransport) Close() {}

// benchFleetDialer hands each endpoint its own transport so one benchmark can
// block or count individual members.
type benchFleetDialer struct {
	transports map[string]*benchFleetTransport
}

func (d *benchFleetDialer) DialContext(_ context.Context, rawURL string) (upstream.Transport, error) {
	return d.transports[rawURL], nil
}

// benchFleet is one voting basis of n members over one shared dialer.
type benchFleet struct {
	p          *Pool
	dialer     *benchFleetDialer
	members    []*Source
	transports []*benchFleetTransport
}

func newBenchFleet(tb testing.TB, n int) *benchFleet {
	tb.Helper()
	f := &benchFleet{
		dialer:     &benchFleetDialer{transports: make(map[string]*benchFleetTransport, n)},
		transports: make([]*benchFleetTransport, n),
	}
	for i := range n {
		url := "https://fleet-" + string(rune('a'+i)) + ".bench"
		tr := &benchFleetTransport{result: json.RawMessage(benchResultPayload)}
		f.transports[i] = tr
		f.dialer.transports[url] = tr
	}
	f.p = newPoolWithDialerAndChainID(f.dialer, 0)
	for i := range n {
		url := "https://fleet-" + string(rune('a'+i)) + ".bench"
		src, err := f.p.join("fleet-"+string(rune('a'+i)), url)
		if err != nil {
			tb.Fatal(err)
		}
		f.members = append(f.members, src)
	}
	tb.Cleanup(f.p.Close)
	return f
}

// warmPace drives successful rounds until every member's scheduler reaches
// its steady-state pace floor, keeping the timed window out of the initial
// 20ms backoff.
func (f *benchFleet) warmPace(b *testing.B, rounds int) {
	b.Helper()
	for range rounds {
		if _, err := f.p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{Timeout: 5 * time.Second}); err != nil {
			b.Fatal(err)
		}
	}
}

// assertSlotFacts pins the slot/attempted/health contract of one collection
// outside the timed loop, so a benchmark that silently stopped exercising the
// intended path fails instead of reporting a misleading baseline.
func assertVotingSlots(b *testing.B, f *benchFleet, emissions []Emission, err error, wantAttempted int) {
	b.Helper()
	if err != nil {
		b.Fatalf("collection verdict: %v", err)
	}
	if len(emissions) != len(f.members) {
		b.Fatalf("slot count = %d, want %d", len(emissions), len(f.members))
	}
	attempted := 0
	for i, em := range emissions {
		if em.Src != f.members[i] {
			b.Fatalf("slot %d carries the wrong member", i)
		}
		if em.Attempted {
			attempted++
		}
	}
	if attempted != wantAttempted {
		b.Fatalf("attempted = %d, want %d", attempted, wantAttempted)
	}
}

// BenchmarkCollectVotingHealthyBasis is the steady-state cost of the voting
// fan-out at 1, 3, and 5 members: every slot is attempted, every emission is
// answered, and health accounting completes once per attempt. ns/op includes
// the per-source scheduler's steady-state pacing; allocs/op is the pool
// machinery itself.
func BenchmarkCollectVotingHealthyBasis(b *testing.B) {
	for _, members := range []int{1, 3, 5} {
		b.Run(fmt.Sprintf("members-%d", members), func(b *testing.B) {
			f := newBenchFleet(b, members)
			f.warmPace(b, 40)
			var before []uint64
			var wireBefore []int64
			for _, src := range f.members {
				before = append(before, src.Health().Successes)
			}
			for _, tr := range f.transports {
				wireBefore = append(wireBefore, tr.requests.Load())
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				emissions, err := f.p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{})
				if err != nil {
					b.Fatal(err)
				}
				lastEmissions = emissions
			}
			b.StopTimer()
			assertVotingSlots(b, f, lastEmissions, nil, members)
			wire := int64(0)
			for i, tr := range f.transports {
				wire += tr.requests.Load() - wireBefore[i]
			}
			successes := f.members[0].Health().Successes - before[0]
			b.ReportMetric(float64(wire)/float64(b.N), "wire-req/op")
			b.ReportMetric(float64(successes)/float64(b.N), "health-successes/op")
		})
	}
}

// lastEmissions keeps one collection's emissions alive past the timed loop so
// the slot assertions can inspect them without paying the check inside the
// measurement window.
var lastEmissions []Emission

// BenchmarkCollectVotingAllGated measures the paused-basis path: every member
// sits behind an open cooldown circuit, so no request is dispatched, no
// upstream is contacted, and every slot still reports the structured gate.
func BenchmarkCollectVotingAllGated(b *testing.B) {
	f := newBenchFleet(b, 3)
	for _, src := range f.members {
		for range eligibleFailureThreshold {
			src.stats.observeFailure(errors.New("bench: open the circuit"))
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		emissions, err := f.p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{})
		if !errors.Is(err, ErrNoEligibleSource) {
			b.Fatalf("all-gated verdict = %v, want ErrNoEligibleSource", err)
		}
		lastEmissions = emissions
	}
	b.StopTimer()
	if len(lastEmissions) != 3 {
		b.Fatalf("slot count = %d, want the full basis", len(lastEmissions))
	}
	for i, em := range lastEmissions {
		if em.Attempted || !errors.Is(em.Err, ErrSourceGated) || em.Src != f.members[i] {
			b.Fatalf("slot %d = %+v, want an unattempted gate on its own member", i, em)
		}
	}
	wire := int64(0)
	for _, tr := range f.transports {
		wire += tr.requests.Load()
	}
	if wire != 0 {
		b.Fatalf("all-gated basis contacted upstream %d times, want 0", wire)
	}
	b.ReportMetric(0, "wire-req/op")
}

// BenchmarkCollectVotingPartialGate measures the mixed basis the voting path
// exists for: two members behind cooldown circuits keep their slots while the
// remaining three are attempted and answered in the same wave.
func BenchmarkCollectVotingPartialGate(b *testing.B) {
	f := newBenchFleet(b, 5)
	for _, src := range f.members[:2] {
		for range eligibleFailureThreshold {
			src.stats.observeFailure(errors.New("bench: open the circuit"))
		}
	}
	// Warm the attemptable members' pacing without disturbing the circuits.
	for range 40 {
		if _, err := f.p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{}); err != nil {
			b.Fatal(err)
		}
	}
	before := f.members[2].Health().Successes
	wireBefore := int64(0)
	for _, tr := range f.transports[2:] {
		wireBefore += tr.requests.Load()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		emissions, err := f.p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{})
		if err != nil {
			b.Fatal(err)
		}
		lastEmissions = emissions
	}
	b.StopTimer()
	assertVotingSlots(b, f, lastEmissions, nil, 3)
	if lastEmissions[0].Attempted || !errors.Is(lastEmissions[0].Err, ErrSourceGated) ||
		lastEmissions[1].Attempted || !errors.Is(lastEmissions[1].Err, ErrSourceGated) {
		b.Fatalf("gated slots lost their pause: %+v %+v", lastEmissions[0], lastEmissions[1])
	}
	wire := int64(0)
	for _, tr := range f.transports[2:] {
		wire += tr.requests.Load()
	}
	b.ReportMetric(float64(wire-wireBefore)/float64(b.N), "wire-req/op")
	b.ReportMetric(float64(f.members[2].Health().Successes-before)/float64(b.N), "health-successes/attempted-op")
}

// BenchmarkCollectVotingCancelledWhileWaiting parks the fleet's transports
// behind a single concurrency slot and cancels the caller once exactly one
// request has crossed the wire: the parked member's exchange returns the
// caller's cancellation, the two slot waiters give up without contacting
// upstream, and no member records a health failure for the caller's own
// cancellation.
func BenchmarkCollectVotingCancelledWhileWaiting(b *testing.B) {
	f := newBenchFleet(b, 3)
	// Warm pacing before parking any request: a parked warm-up call would
	// otherwise hit the fan-out's own deadline.
	f.warmPace(b, 40)
	hold := make(chan struct{})
	for _, tr := range f.transports {
		tr.hold.Store(hold)
	}
	fleetWatermark := func() int64 {
		total := int64(0)
		for _, tr := range f.transports {
			total += tr.requests.Load()
		}
		return total
	}
	before := fleetWatermark()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		watermark := fleetWatermark()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			emissions, err := f.p.CollectVoting(ctx, "eth_blockNumber", nil, CollectOptions{MaxConcurrent: 1})
			lastCancelErr, lastCancelEmissions = err, emissions
		}()
		// With one slot, the single request that crossed the wire belongs to
		// the sem holder; every other member is waiting for that slot.
		for fleetWatermark() == watermark {
			time.Sleep(time.Microsecond)
		}
		cancel()
		<-done
	}
	b.StopTimer()
	close(hold)
	if !errors.Is(lastCancelErr, context.Canceled) {
		b.Fatalf("verdict = %v, want the caller's cancellation", lastCancelErr)
	}
	if len(lastCancelEmissions) != 3 {
		b.Fatalf("slots = %d, want the full basis despite cancellation", len(lastCancelEmissions))
	}
	attempted := 0
	for _, em := range lastCancelEmissions {
		if em.Attempted {
			attempted++
			if !errors.Is(em.Err, context.Canceled) {
				b.Fatalf("attempted slot err = %v, want the caller's cancellation", em.Err)
			}
		}
	}
	if attempted != 1 {
		b.Fatalf("attempted slots = %d, want exactly the one in-flight member", attempted)
	}
	if got := fleetWatermark() - before; got != int64(b.N) {
		b.Fatalf("cancelled rounds contacted upstream %d times, want exactly %d (one parked exchange per round)", got, b.N)
	}
}

var (
	lastCancelErr       error
	lastCancelEmissions []Emission
)

// BenchmarkCollectVotingClosedMember measures the captured-basis slot that
// failed closed before the wave started: ErrClosed emission, no upstream
// contact, no health accounting, and the live members still served.
func BenchmarkCollectVotingClosedMember(b *testing.B) {
	f := newBenchFleet(b, 3)
	f.members[0].close()
	f.warmPace(b, 40)
	wireBefore := int64(0)
	for _, tr := range f.transports[1:] {
		wireBefore += tr.requests.Load()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		emissions, err := f.p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{})
		if err != nil {
			b.Fatal(err)
		}
		lastEmissions = emissions
	}
	b.StopTimer()
	assertVotingSlots(b, f, lastEmissions, nil, 2)
	if lastEmissions[0].Attempted || !errors.Is(lastEmissions[0].Err, ErrClosed) {
		b.Fatalf("closed slot = %+v, want ErrClosed with no attempt", lastEmissions[0])
	}
	if f.transports[0].requests.Load() != 0 {
		b.Fatalf("closed member contacted upstream %d times", f.transports[0].requests.Load())
	}
	wire := int64(0)
	for _, tr := range f.transports[1:] {
		wire += tr.requests.Load()
	}
	b.ReportMetric(float64(wire-wireBefore)/float64(b.N), "wire-req/op")
}

package pool

import (
	"context"
	"encoding/json"
	"math/big"
	"sync"
	"testing"
	"time"
)

// samplerPeer is a test transport for the tip-sampler lifecycle: the first
// call warms the connection, later calls report themselves on entered and
// block until released or canceled. A probe that was canceled still spends
// cleanup before returning and announces its return, so a Close that waits
// for the pool-owned sampler cannot return before the probe did.
type samplerPeer struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
	returne chan time.Time
	cleanup time.Duration
}

func (p *samplerPeer) CallContext(ctx context.Context, result any, _ string, _ ...any) error {
	p.mu.Lock()
	p.calls++
	warm := p.calls == 1
	p.mu.Unlock()
	if warm {
		*result.(*json.RawMessage) = json.RawMessage(`"0x1"`)
		return nil
	}
	p.entered <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	if p.cleanup > 0 {
		time.Sleep(p.cleanup)
	}
	select {
	case p.returne <- time.Now():
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	*result.(*json.RawMessage) = json.RawMessage(`"0x1"`)
	return nil
}

func (*samplerPeer) ChainID(context.Context) (*big.Int, error) { return big.NewInt(1), nil }
func (*samplerPeer) Close()                                    {}

func (p *samplerPeer) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func samplerPool(t *testing.T, every time.Duration, peer *samplerPeer) *Pool {
	t.Helper()
	p, err := NewWithMembers(1, []Member{{
		Name:   "provider",
		URL:    "https://provider.example",
		Dialer: &contractDialer{transport: peer},
	}}, WithTipTracking(every, 16))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func warmSamplerPool(t *testing.T, p *Pool, peer *samplerPeer, ctx context.Context) {
	t.Helper()
	// Warm so probes observe an established connection instead of dialing.
	if _, err := p.Collect(ctx, "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatalf("warm call: %v", err)
	}
	if peer.callCount() != 1 {
		t.Fatalf("warm call count = %d, want 1", peer.callCount())
	}
}

// Close stops the sampler: after it returns, neither the background loop nor
// the manual refresh path may start another probe, and a repeated Close
// stays inert.
func TestCloseStopsTipSamplerAndRefusesLaterProbes(t *testing.T) {
	peer := &samplerPeer{entered: make(chan struct{}, 16), release: make(chan struct{}), returne: make(chan time.Time, 1)}
	p := samplerPool(t, 15*time.Millisecond, peer)
	t.Cleanup(func() { p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	warmSamplerPool(t, p, peer, ctx)
	for peer.callCount() < 3 {
		select {
		case <-peer.entered:
		case <-ctx.Done():
			t.Fatal("tip sampler never probed")
		}
	}

	p.Close()
	afterClose := peer.callCount()
	// Several sampling intervals must pass without a new probe.
	time.Sleep(10 * 15 * time.Millisecond)
	if got := peer.callCount(); got != afterClose {
		t.Fatalf("probes ran after Close: %d -> %d", afterClose, got)
	}
	p.RefreshTips(ctx)
	if got := peer.callCount(); got != afterClose {
		t.Fatalf("manual refresh dialed after Close: %d -> %d", afterClose, got)
	}
	p.Close() // idempotent
	if got := peer.callCount(); got != afterClose {
		t.Fatalf("second Close started a probe: %d -> %d", afterClose, got)
	}
}

// Close waits for the pool-owned sampler: while one of its probes is still
// finishing a bounded exit, Close must not return, and afterwards no probe
// may run again.
func TestCloseWaitsForInFlightSamplerProbe(t *testing.T) {
	peer := &samplerPeer{entered: make(chan struct{}, 4), release: make(chan struct{}), returne: make(chan time.Time, 1), cleanup: 150 * time.Millisecond}
	p := samplerPool(t, 50*time.Millisecond, peer)
	t.Cleanup(func() { p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	warmSamplerPool(t, p, peer, ctx)
	select {
	case <-peer.entered:
	case <-ctx.Done():
		t.Fatal("tip sampler never probed")
	}
	closeDone := make(chan time.Time, 1)
	go func() {
		p.Close()
		closeDone <- time.Now()
	}()
	var probeReturned time.Time
	select {
	case probeReturned = <-peer.returne:
	case <-ctx.Done():
		t.Fatal("in-flight probe never finished its bounded exit")
	}
	var closeReturned time.Time
	select {
	case closeReturned = <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the sampler exited")
	}
	if closeReturned.Before(probeReturned) {
		t.Fatalf("Close returned at %v, before the in-flight sampler probe finished at %v", closeReturned, probeReturned)
	}
	afterClose := peer.callCount()
	time.Sleep(5 * 50 * time.Millisecond)
	if got := peer.callCount(); got != afterClose {
		t.Fatalf("probes ran after Close: %d -> %d", afterClose, got)
	}
}

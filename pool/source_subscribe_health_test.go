package pool

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	upstream "github.com/codermaybe/ethguard/internal/transport"
)

// subscribingDeadTransport answers the chain identity probe but fails every
// subscribe attempt with the connection-closed sentinel, mimicking a WSS
// transport whose socket died while remaining cached on the source.
type subscribingDeadTransport struct {
	contractTransport

	mu   sync.Mutex
	subs int
}

func (t *subscribingDeadTransport) SubscribeCapability(context.Context, string, ...any) (upstream.Subscription, error) {
	t.mu.Lock()
	t.subs++
	t.mu.Unlock()
	return nil, fmt.Errorf("wss: write failed: %w", upstream.ErrConnectionClosed)
}

// TestSubscribeDiscardsDeadTransportSoReconnectRedials pins the subscription
// half of the unary transport-hygiene contract: a subscribe attempt that
// fails on a closed connection must discard the cached transport so the next
// attempt redials, and must count toward source health like the unary paths
// do. Without the discard, the reconnect loop keeps hitting the same dead
// transport forever and the subscription stalls silently.
func TestSubscribeDiscardsDeadTransportSoReconnectRedials(t *testing.T) {
	tr := &subscribingDeadTransport{}
	tr.chainID = big.NewInt(1)
	dialer := &contractDialer{transport: tr}
	p := newPoolWithDialerAndChainID(dialer, 1)
	defer p.Close()

	source, err := p.join("a", "wss://a.example")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := source.Subscribe(context.Background(), "newHeads"); err == nil {
			t.Fatal("expected the dead transport's subscribe to fail")
		}
	}
	if got := dialer.dialCount(); got != 3 {
		t.Fatalf("dead transport never discarded: dials=%d, want 3", got)
	}
	if snap := source.Health(); snap.ConsecutiveFailures != 3 {
		t.Fatalf("subscribe failures not counted toward source health: consecutive=%d", snap.ConsecutiveFailures)
	}
}

// hangingTransport answers the identity probe and then blocks inside
// CallContext until the caller's context is canceled, so the cancellation
// deterministically lands mid-call.
type hangingTransport struct {
	contractTransport

	entered chan struct{}
}

func (t *hangingTransport) CallContext(ctx context.Context, _ any, _ string, _ ...any) error {
	select {
	case t.entered <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestCallContextDoesNotBlameSourceForCallerCancellation extends the unary
// health-exemption contract (TestCollectDoesNotBlameSourceForCallerCancellation)
// to the same-source Pull seam: a call that ends because the caller went away
// must not count against the source's consecutive-failure budget, or a batch
// of canceled pull probes cools down a healthy source for the next fan-out.
func TestCallContextDoesNotBlameSourceForCallerCancellation(t *testing.T) {
	tr := &hangingTransport{entered: make(chan struct{}, 1)}
	tr.chainID = big.NewInt(1)
	dialer := &contractDialer{transport: tr}
	p := newPoolWithDialerAndChainID(dialer, 1)
	defer p.Close()

	source, err := p.join("a", "wss://a.example")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		err error
	}
	results := make(chan result, 1)
	go func() {
		_, err := source.CallContext(ctx, "eth_getBlockByNumber", "0x1", false)
		results <- result{err: err}
	}()
	select {
	case <-tr.entered:
	case <-time.After(time.Second):
		t.Fatal("call never reached the transport")
	}
	cancel()
	select {
	case res := <-results:
		if res.err == nil {
			t.Fatal("expected the canceled call to fail")
		}
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("unexpected call error: %v", res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled call never returned")
	}
	if snap := source.Health(); snap.ConsecutiveFailures != 0 {
		t.Fatalf("caller cancellation counted as source failure: consecutive=%d", snap.ConsecutiveFailures)
	}
}

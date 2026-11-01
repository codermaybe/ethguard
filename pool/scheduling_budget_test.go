package pool

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"
)

// budgetPeer is a controlled upstream, not a provider compatibility sample.
type budgetPeer struct {
	entered chan string
	release chan struct{}
	calls   atomic.Int32
}

func (p *budgetPeer) CallContext(ctx context.Context, result any, method string, _ ...any) error {
	p.calls.Add(1)
	p.entered <- method
	select {
	case <-p.release:
		*result.(*json.RawMessage) = json.RawMessage(`"0x1"`)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*budgetPeer) ChainID(context.Context) (*big.Int, error) { return big.NewInt(1), nil }
func (*budgetPeer) Close()                                    {}

func TestSourceBudgetSharedByReadWriteAndAdmission(t *testing.T) {
	peer := &budgetPeer{entered: make(chan string, 16), release: make(chan struct{})}
	p, err := NewWithMembers(1, []Member{{Name: "provider", URL: "https://provider.example", Dialer: &contractDialer{transport: peer}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	source := p.VotingMembers()[0]
	if source.Health().RequestInFlightLimit != 2 {
		t.Fatal("scenario requires the current two-slot source budget")
	}
	completed := make(chan error, 2)
	go func() {
		_, err := p.CollectVoting(ctx, "eth_getBlockByNumber", []any{"latest", false}, CollectOptions{})
		completed <- err
	}()
	go func() {
		_, err := p.Broadcast(ctx, "eth_sendRawTransaction", []any{"0x01"})
		completed <- err
	}()
	for range 2 {
		select {
		case <-peer.entered:
		case <-ctx.Done():
			t.Fatal("read/write did not occupy the source budget")
		}
	}
	for _, route := range []string{"voting read", "selected read", "forward", "write", "admission"} {
		t.Run(route, func(t *testing.T) {
			waitCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
			defer stop()
			var emissions []Emission
			var err error
			switch route {
			case "voting read":
				emissions, err = p.CollectVoting(waitCtx, "eth_getBalance", nil, CollectOptions{})
			case "selected read":
				emissions, err = p.Collect(waitCtx, "eth_blockNumber", nil, CollectOptions{Sources: []*Source{source}})
			case "forward":
				emissions, err = p.Forward(waitCtx, "eth_probe", nil, CollectOptions{})
			case "write":
				emissions, err = p.Broadcast(waitCtx, "eth_sendRawTransaction", nil)
			case "admission":
				_, err = p.CallSource(waitCtx, source, "eth_getUncleByBlockHashAndIndex", nil)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("budget wait err = %v", err)
			}
			if route != "admission" && (len(emissions) != 1 || emissions[0].Attempted) {
				t.Fatalf("queued member disappeared or was sent: %+v", emissions)
			}
			if peer.calls.Load() != 2 {
				t.Fatalf("route bypassed the budget: calls = %d", peer.calls.Load())
			}
			if h := source.Health(); h.Successes != 0 || h.Failures != 0 || h.RequestInFlight != 2 {
				t.Fatalf("local wait changed health or leaked a slot: %+v", h)
			}
		})
	}
	close(peer.release)
	for range 2 {
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("held calls did not finish")
		}
	}
	emissions, err := p.CollectVoting(ctx, "eth_getBalance", nil, CollectOptions{})
	if err != nil || len(emissions) != 1 || !emissions[0].Attempted || emissions[0].Err != nil {
		t.Fatalf("budget did not recover: %+v, err = %v", emissions, err)
	}
	if h := source.Health(); h.Successes != 3 || h.Failures != 0 || h.RequestInFlight != 0 || peer.calls.Load() != 3 {
		t.Fatalf("queued call was replayed or leaked a slot: %+v, calls = %d", h, peer.calls.Load())
	}
}

package pool

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"
)

// contractTransport is deliberately independent of go-ethereum. These tests
// verify the pool seam itself, rather than the compatibility adapter that
// currently implements it.
type contractTransport struct {
	mu sync.Mutex

	chainID      *big.Int
	result       json.RawMessage
	err          error
	calls        int
	chainIDCalls int
	closeCalls   int
}

func (t *contractTransport) CallContext(ctx context.Context, result any, _ string, _ ...any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	t.calls++
	err := t.err
	value := append(json.RawMessage(nil), t.result...)
	t.mu.Unlock()
	if err != nil {
		return err
	}
	out, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("contract transport: result is not *json.RawMessage")
	}
	*out = value
	return nil
}

func (t *contractTransport) ChainID(context.Context) (*big.Int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.chainIDCalls++
	if t.chainID == nil {
		return nil, errors.New("contract transport: missing chain id")
	}
	return new(big.Int).Set(t.chainID), nil
}

func (t *contractTransport) Close() {
	t.mu.Lock()
	t.closeCalls++
	t.mu.Unlock()
}

func (t *contractTransport) counts() (calls, chainIDCalls, closeCalls int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls, t.chainIDCalls, t.closeCalls
}

type contractDialer struct {
	mu        sync.Mutex
	transport Transport
	dials     int
}

func (d *contractDialer) DialContext(context.Context, string) (Transport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	return d.transport, nil
}

func (d *contractDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func TestTransportSeamAcceptsIndependentAdapterAndReusesIt(t *testing.T) {
	transport := &contractTransport{
		chainID: new(big.Int).SetInt64(1),
		result:  json.RawMessage(`"0x2a"`),
	}
	dialer := &contractDialer{transport: transport}
	p := newPoolWithDialerAndChainID(dialer, 1)
	src, err := p.Join("contract", "https://contract.example")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{Sources: []*Source{src}})
		if err != nil {
			t.Fatalf("Collect[%d]: %v", i, err)
		}
		if len(emissions) != 1 || string(emissions[0].Val) != `"0x2a"` {
			t.Fatalf("Collect[%d] emissions = %+v, want one 0x2a value", i, emissions)
		}
	}

	if got := dialer.dialCount(); got != 1 {
		t.Fatalf("dial count = %d, want one shared dial", got)
	}
	calls, chainIDCalls, closeCalls := transport.counts()
	if calls != 2 || chainIDCalls != 1 || closeCalls != 0 {
		t.Fatalf("transport counts = calls:%d chainID:%d close:%d, want 2/1/0", calls, chainIDCalls, closeCalls)
	}

	p.Close()
	_, _, closeCalls = transport.counts()
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want one close from Pool.Close", closeCalls)
	}
	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Collect after Close = %v, want ErrClosed", err)
	}
}

func TestTransportSeamChainIDMismatchStopsCall(t *testing.T) {
	transport := &contractTransport{
		chainID: new(big.Int).SetInt64(2),
		result:  json.RawMessage(`"0x2a"`),
	}
	dialer := &contractDialer{transport: transport}
	p := newPoolWithDialerAndChainID(dialer, 1)
	if _, err := p.Join("contract", "https://contract.example"); err != nil {
		t.Fatal(err)
	}

	emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if !errors.Is(err, ErrChainIDMismatch) {
		t.Fatalf("Collect error = %v, want ErrChainIDMismatch", err)
	}
	if len(emissions) != 1 || !errors.Is(emissions[0].Err, ErrChainIDMismatch) {
		t.Fatalf("emissions = %+v, want one chain ID mismatch", emissions)
	}
	calls, chainIDCalls, _ := transport.counts()
	if calls != 0 || chainIDCalls != 1 {
		t.Fatalf("transport counts = calls:%d chainID:%d, want 0/1", calls, chainIDCalls)
	}
}

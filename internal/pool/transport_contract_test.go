package pool

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"

	upstream "github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
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

func (t *contractTransport) Call(ctx context.Context, _ string, _ ...any) rpc.Response {
	if err := ctx.Err(); err != nil {
		return rpc.Response{Exchange: rpc.Exchange{Cause: err}}
	}
	t.mu.Lock()
	t.calls++
	err := t.err
	value := append(json.RawMessage(nil), t.result...)
	t.mu.Unlock()
	if err != nil {
		return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: err}}
	}
	return rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: value}}
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
	transport upstream.Transport
	dials     int
}

func (d *contractDialer) DialContext(context.Context, string) (upstream.Transport, error) {
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
	p := testPool(t, 1, WithDialer(dialer))
	src, err := p.join("contract", "https://contract.example")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{Sources: []*Source{src}})
		if err != nil {
			t.Fatalf("Collect[%d]: %v", i, err)
		}
		if len(emissions) != 1 || string(emissions[0].Response.Answer.Result) != `"0x2a"` {
			t.Fatalf("Collect[%d] emissions = %+v, want one 0x2a value", i, emissions)
		}
		response := emissions[0].Response
		if response.Err() != nil || response.Exchange.Progress != rpc.ExchangeCompleted || response.Answer.Kind != rpc.AnswerResult || string(response.Answer.Result) != `"0x2a"` {
			t.Fatalf("transport facts were lost: %+v", response)
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
	p := testPool(t, 1, WithDialer(dialer))
	if _, err := p.join("contract", "https://contract.example"); err != nil {
		t.Fatal(err)
	}

	emissions, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if !errors.Is(err, ErrChainIDMismatch) {
		t.Fatalf("Collect error = %v, want ErrChainIDMismatch", err)
	}
	if len(emissions) != 1 || !errors.Is(emissions[0].Response.Err(), ErrChainIDMismatch) {
		t.Fatalf("emissions = %+v, want one chain ID mismatch", emissions)
	}
	calls, chainIDCalls, _ := transport.counts()
	if calls != 0 || chainIDCalls != 1 {
		t.Fatalf("transport counts = calls:%d chainID:%d, want 0/1", calls, chainIDCalls)
	}
}

func TestEmissionRetainsInterruptedAnswer(t *testing.T) {
	response := rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted, Cause: context.Canceled}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`null`)}}
	e := Emission{Response: response, Attempted: true}
	if !errors.Is(e.Response.Err(), context.Canceled) || e.Response.Answer.Kind != rpc.AnswerResult || string(e.Response.Answer.Result) != "null" {
		t.Fatalf("response lost: %+v", e)
	}
}

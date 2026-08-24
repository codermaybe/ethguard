package aggregator

import (
	"context"
	"testing"

	"ethguard/pool"
	"ethguard/rpc"
)

func TestAggregateEmptyPoolReturnsCollectionError(t *testing.T) {
	a := New(pool.NewPool(), nil)
	_, err := a.Aggregate(context.Background(), rpc.Request{Method: "eth_chainId"})
	if err == nil {
		t.Fatal("expected empty pool to fail")
	}
}

func TestAggregateAttachesSourceFaultsToDecision(t *testing.T) {
	p := pool.NewPool()
	if _, err := p.Join("dead", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	a := New(p, nil)
	decision, err := a.Aggregate(context.Background(), rpc.Request{Method: "eth_unknown"})
	if err == nil {
		t.Fatal("expected unreachable source to fail the call")
	}
	if len(decision.Faults) == 0 {
		t.Fatal("expected collection faults on decision, got none")
	}
}

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

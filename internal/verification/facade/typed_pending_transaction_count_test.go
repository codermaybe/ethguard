package ethguard_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func TestPendingTransactionCountIsSelectedAndUnanchored(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByNumber, "0x2")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, result, err := client.PendingTransactionCount(ctx)
	if err != nil || count != 2 {
		t.Fatalf("pending transaction count = %d, result=%+v, err=%v", count, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("pending transaction count decision = %+v, want Selected", result.Decision)
	}
	if result.Decision.Reference != nil || result.Decision.BlockRef != "" {
		t.Fatalf("pending transaction count established reference: %+v", result.Decision)
	}
	viewCount, viewErr := client.EthClient().PendingTransactionCount(ctx)
	if viewErr != nil || viewCount != 2 {
		t.Fatalf("view pending transaction count = %d, err=%v", viewCount, viewErr)
	}
}

func TestPendingTransactionCountNullIsMalformed(t *testing.T) {
	client, nodes := fleet(t, 1)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByNumber, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, result, err := client.PendingTransactionCount(ctx)
	if count != 0 || result.Decision == nil || err == nil || !errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("pending null = count:%d result:%+v err:%v, want no-valid-response", count, result, err)
	}
}

package ethguard_test

import (
	"context"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func TestTransactionCountCorroboratesAndProjectsView(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByHash, "0x2")
	}
	ctx := context.Background()
	hash := common.HexToHash("0x" + "11")
	count, result, err := client.TransactionCount(ctx, hash)
	if err != nil || count != 2 || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("root TransactionCount = %d, result=%+v, err=%v", count, result, err)
	}
	viewCount, err := client.EthClient().TransactionCount(ctx, hash)
	if err != nil || viewCount != count {
		t.Fatalf("view TransactionCount = %d, err=%v; root=%d", viewCount, err, count)
	}
}

func TestTransactionCountNullMapsToNotFound(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockTransactionCountByHash, nil)
	}
	hash := common.HexToHash("0x" + "22")
	count, result, err := client.TransactionCount(context.Background(), hash)
	if count != 0 || result.Decision == nil || !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("root null TransactionCount = %d, result=%+v, err=%v", count, result, err)
	}
	viewCount, viewErr := client.EthClient().TransactionCount(context.Background(), hash)
	if viewCount != 0 || !errors.Is(viewErr, ethereum.NotFound) {
		t.Fatalf("view null TransactionCount = %d, err=%v", viewCount, viewErr)
	}
}

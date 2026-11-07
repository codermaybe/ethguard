package ethguard_test

import (
	"context"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard"
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

func TestTransactionCountMultiSourceConflictKeepsEvidence(t *testing.T) {
	client, nodes := fleet(t, 2)
	nodes[0].Return(methods.MethodGetBlockTransactionCountByHash, "0x1")
	nodes[1].Return(methods.MethodGetBlockTransactionCountByHash, "0x2")

	count, result, err := client.TransactionCount(context.Background(), common.HexToHash("0x23"))
	if count != 0 || !errors.Is(err, ethguard.ErrConflict) || result.Decision == nil {
		t.Fatalf("conflict = count:%d result:%+v err:%v", count, result, err)
	}
	if result.Decision.Status != rpc.Unknown || len(result.Decision.Support) != 1 || len(result.Decision.Reject) != 1 {
		t.Fatalf("conflict decision = %+v, want unknown with one support and one reject", result.Decision)
	}
	if result.Decision.Support[0].ID == "" || result.Decision.Reject[0].ID == "" || result.Decision.Support[0].ID == result.Decision.Reject[0].ID {
		t.Fatalf("conflict sources = support:%+v reject:%+v", result.Decision.Support, result.Decision.Reject)
	}
}

func TestTransactionCountMultiSourceMalformedFailsWithoutValue(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodGetBlockTransactionCountByHash, []byte(`"not-a-quantity"`))
	}

	count, result, err := client.TransactionCount(context.Background(), common.HexToHash("0x24"))
	if count != 0 || !errors.Is(err, ethguard.ErrNoValidResponse) || result.Decision == nil {
		t.Fatalf("malformed = count:%d result:%+v err:%v", count, result, err)
	}
	if result.Decision.Status != rpc.Unknown || len(result.Decision.Support) != 0 || len(result.Decision.Faults) != 3 {
		t.Fatalf("malformed decision = %+v, want unknown with three faults", result.Decision)
	}
}

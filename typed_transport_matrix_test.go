package ethguard_test

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum/common"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// TestTypedTransportMatrix exercises the same typed reads through native HTTP
// and the raw WSS adapter. sourceWirePeer is deliberately shared with the
// mixed-source tests, so this closes the transport-to-execution seam without
// adding a second server or a subscription fixture.
func TestTypedTransportMatrix(t *testing.T) {
	rawBlock := fixtureBlock(t, 17)
	receiptHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	rawReceipt := fixtureReceipt(t, receiptHash, 17)
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			if req.Method == methods.MethodGetTransactionReceipt {
				return `"result":` + string(rawReceipt)
			}
			if req.Method == methods.MethodGetBlockReceipts {
				return `"result":[` + string(rawReceipt) + `]`
			}
			return `"result":"0x1"`
		}),
		newSourceWirePeer(t, true, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			if req.Method == methods.MethodGetTransactionReceipt {
				return `"result":` + string(rawReceipt)
			}
			if req.Method == methods.MethodGetBlockReceipts {
				return `"result":[` + string(rawReceipt) + `]`
			}
			return `"result":"0x1"`
		}),
	}
	client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
		{ID: "http", Endpoint: peers[0].endpoint},
		{ID: "wss", Endpoint: peers[1].endpoint, Transport: config.TransportWSS},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, _, err := client.ChainID(ctx)
	if err != nil || id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ChainID = %v, err=%v", id, err)
	}
	header, _, err := client.HeaderByNumber(ctx, big.NewInt(17))
	if err != nil || header == nil || header.Number.Uint64() != 17 {
		t.Fatalf("HeaderByNumber = %v, err=%v", header, err)
	}
	receipt, _, err := client.TransactionReceipt(ctx, receiptHash)
	if err != nil || receipt == nil || receipt.TxHash != receiptHash {
		t.Fatalf("TransactionReceipt = %v, err=%v", receipt, err)
	}
	receipts, _, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(receipt.BlockHash, false))
	if err != nil || len(receipts) != 1 || receipts[0].TxHash != receiptHash {
		t.Fatalf("BlockReceipts = %v, err=%v", receipts, err)
	}
	if peers[0].httpConnections.Load() == 0 || peers[1].connections.Load() == 0 {
		t.Fatalf("transport connections = http:%d wss:%d", peers[0].httpConnections.Load(), peers[1].connections.Load())
	}
	// Both transports must have received the identity probe and the typed
	// header request. Track those methods independently from the aggregate
	// request total, so a connection or unrelated request cannot satisfy this
	// seam test accidentally. The WSS peer uses the same JSON-RPC request bytes
	// as HTTP; its frame lifecycle is covered by internal/transport/wss tests.
	for i, peer := range peers {
		if peer.chainIDRequests.Load() == 0 || peer.headerRequests.Load() == 0 {
			t.Fatalf("peer %d method counts = chainID:%d header:%d total:%d, want identity + header", i, peer.chainIDRequests.Load(), peer.headerRequests.Load(), peer.requests.Load())
		}
	}
}

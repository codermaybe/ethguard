package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var receiptWait = 3 * time.Second

// syncReceiptFixture marshals one mined receipt for a transaction hash into
// the wire shape the receipt admission layer accepts.
func syncReceiptFixture(t *testing.T, txHash common.Hash, height int64) json.RawMessage {
	t.Helper()
	receipt := &types.Receipt{
		Type:              types.LegacyTxType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21000,
		Logs: []*types.Log{{
			Address:     common.HexToAddress("0x" + strings.Repeat("cc", 20)),
			Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("dd", 32))},
			Data:        []byte{0x01},
			BlockNumber: uint64(height),
			TxHash:      txHash,
			TxIndex:     1,
			BlockHash:   common.HexToHash("0x" + strings.Repeat("11", 32)),
			Index:       0,
		}},
		TxHash:            txHash,
		GasUsed:           21000,
		BlockHash:         common.HexToHash("0x" + strings.Repeat("11", 32)),
		BlockNumber:       big.NewInt(height),
		TransactionIndex:  1,
		EffectiveGasPrice: big.NewInt(1_000_000_000),
	}
	receipt.Bloom = types.CreateBloom(receipt)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestSendRawTransactionSyncLocalValidationPins pins the local-only
// rejections of the synchronous write entry: a negative receipt timeout and
// an undecodable payload fail before any wire contact, with the classified
// sentinel the write contract promises.
func TestSendRawTransactionSyncLocalValidationPins(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 2)
	raw, _ := writeTxFixture(t)
	negative := -1 * time.Second
	if _, _, err := client.SendRawTransactionSync(context.Background(), raw, &negative); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("negative timeout error = %v, want ErrInvalidRequest", err)
	}
	if _, _, err := client.SendRawTransactionSync(context.Background(), []byte("not-a-tx"), nil); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("undecodable payload error = %v, want ErrInvalidRequest", err)
	}
	if _, _, err := client.SendTransactionSync(context.Background(), nil, nil); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("nil transaction error = %v, want ErrInvalidRequest", err)
	}
	for i, node := range nodes {
		if calls := countMethod(node.Calls(), methods.MethodSendRawTransaction); calls != 0 {
			t.Fatalf("source %d contacted on a local rejection: %d calls", i, calls)
		}
	}
}

// TestSendRawTransactionSyncWaitsForReceipt pins the full synchronous write:
// broadcast accepted by a quorum, then the shared receipt execution resolves
// the receipt, with the write decision preserved on the returned CallResult.
func TestSendRawTransactionSyncWaitsForReceipt(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, hash := writeTxFixture(t)
	for _, node := range nodes[:2] {
		node.Return(methods.MethodSendRawTransaction, hash.Hex())
	}
	nodes[2].Return(methods.MethodSendRawTransaction, common.HexToHash("0x"+strings.Repeat("11", 32)).Hex())
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, syncReceiptFixture(t, hash, 17))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, result, err := client.SendRawTransactionSync(ctx, raw, &receiptWait)
	if err != nil {
		t.Fatalf("sync write failed: %v", err)
	}
	if receipt == nil || receipt.TxHash != hash {
		t.Fatalf("receipt for the wrong transaction: %+v", receipt)
	}
	if writeDecision(t, result).Status != rpc.WriteAccepted || writeDecision(t, result).Hash != hash.Hex() {
		t.Fatalf("write decision not preserved on the sync result: %+v", writeDecision(t, result))
	}
	if len(result.WriteDecision.Accepted) != 2 {
		t.Fatalf("accepting sources not preserved: %+v", result.WriteDecision.Accepted)
	}
}

// TestSendRawTransactionSyncFailsClosedWhenNotAccepted pins the gate between
// the broadcast and the receipt poll: a write the fleet refused never
// enters receipt waiting, and the classified error is ErrNoValidResponse.
func TestSendRawTransactionSyncFailsClosedWhenNotAccepted(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, _ := writeTxFixture(t)
	for _, node := range nodes {
		node.Fail(methods.MethodSendRawTransaction, -32010, "insufficient funds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, result, err := client.SendRawTransactionSync(ctx, raw, nil)
	if !errors.Is(err, rpc.ErrNoValidResponse) {
		t.Fatalf("rejected write error = %v, want ErrNoValidResponse", err)
	}
	if receipt != nil {
		t.Fatalf("rejected write produced a receipt: %+v", receipt)
	}
	if writeDecision(t, result).Status != rpc.WriteRejected {
		t.Fatalf("rejected write decision = %+v, want WriteRejected", writeDecision(t, result))
	}
	for i, node := range nodes {
		if calls := countMethod(node.Calls(), methods.MethodGetTransactionReceipt); calls != 0 {
			t.Fatalf("source %d polled receipts for a refused write: %d calls", i, calls)
		}
	}
}

// TestSendRawTransactionSyncTimesOutWithoutReceipt pins the polling bound:
// when the transaction never mines, the explicit positive timeout converts
// to a context deadline on the receipt poll, with the write decision still
// reporting acceptance.
func TestSendRawTransactionSyncTimesOutWithoutReceipt(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 2)
	raw, hash := writeTxFixture(t)
	for _, node := range nodes {
		node.Return(methods.MethodSendRawTransaction, hash.Hex())
		node.Return(methods.MethodGetTransactionReceipt, nil) // corroborated null → NotFound, forever
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	short := 300 * time.Millisecond
	started := time.Now()
	receipt, result, err := client.SendRawTransactionSync(ctx, raw, &short)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unmined write error = %v, want context.DeadlineExceeded", err)
	}
	if receipt != nil {
		t.Fatalf("timeout produced a receipt: %+v", receipt)
	}
	if writeDecision(t, result).Status != rpc.WriteAccepted {
		t.Fatalf("write decision lost across the timeout: %+v", writeDecision(t, result))
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("explicit timeout not honored: %s", elapsed)
	}
}

package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func transactionInBlockFixture(t *testing.T, blockHash common.Hash, index uint64) json.RawMessage {
	t.Helper()
	raw := signedTxFixture(t, true)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	blockRaw, _ := json.Marshal(blockHash.Hex())
	indexRaw, _ := json.Marshal("0x" + "10")
	obj["blockHash"] = blockRaw
	if index == 0 {
		indexRaw, _ = json.Marshal("0x0")
	}
	obj["transactionIndex"] = indexRaw
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTransactionInBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := common.HexToHash("0x33")
	raw := transactionInBlockFixture(t, blockHash, 0)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	tx, result, err := client.TransactionInBlock(context.Background(), blockHash, 0)
	if err != nil || tx == nil || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionInBlock = tx:%v result:%+v err:%v", tx, result, err)
	}
	viewTx, viewErr := client.EthClient().TransactionInBlock(context.Background(), blockHash, 0)
	if viewErr != nil || viewTx == nil || viewTx.Hash() != tx.Hash() {
		t.Fatalf("view TransactionInBlock = tx:%v err:%v", viewTx, viewErr)
	}
}

func TestTransactionInBlockNullMapsToNotFound(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := common.HexToHash("0x44")
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, nil)
	}
	if _, _, err := client.TransactionInBlock(context.Background(), blockHash, 0); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("TransactionInBlock null error = %v, want ethereum.NotFound", err)
	}
}

func TestTransactionSender(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := common.HexToHash("0x55")
	raw := transactionInBlockFixture(t, blockHash, 0)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	var tx types.Transaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		t.Fatal(err)
	}
	wantKey, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.PubkeyToAddress(wantKey.PublicKey)
	sender, result, err := client.TransactionSender(context.Background(), &tx, blockHash, 0)
	if err != nil || sender != want || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionSender = %s result=%+v err=%v, want %s", sender, result, err, want)
	}
	viewSender, viewErr := client.EthClient().TransactionSender(context.Background(), &tx, blockHash, 0)
	if viewErr != nil || viewSender != want {
		t.Fatalf("view TransactionSender = %s err=%v, want %s", viewSender, viewErr, want)
	}
}

func TestTransactionSenderRejectsHashMismatch(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := common.HexToHash("0x66")
	raw := transactionInBlockFixture(t, blockHash, 0)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	input := types.NewTransaction(99, common.HexToAddress("0x1"), big.NewInt(1), 21000, big.NewInt(1), nil)
	if _, _, err := client.TransactionSender(context.Background(), input, blockHash, 0); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("hash mismatch error = %v, want malformed response", err)
	}
}

func TestTransactionSenderRejectsMissingFrom(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := common.HexToHash("0x77")
	raw := transactionInBlockFixture(t, blockHash, 0)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "from")
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	var tx types.Transaction
	if err := json.Unmarshal(transactionInBlockFixture(t, blockHash, 0), &tx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.TransactionSender(context.Background(), &tx, blockHash, 0); err == nil {
		t.Fatal("missing sender unexpectedly succeeded")
	}
}

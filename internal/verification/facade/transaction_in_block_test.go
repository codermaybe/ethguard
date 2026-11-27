package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func transactionInBlockFixture(t *testing.T, blockHash ethapi.Hash, index uint64) json.RawMessage {
	t.Helper()
	raw := signedTxFixture(t, true)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	blockRaw, _ := json.Marshal(string(blockHash))
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

func transactionInBlockVariant(t *testing.T, blockHash ethapi.Hash, index, nonce uint64) json.RawMessage {
	t.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTransaction(nonce, common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), big.NewInt(1_000_000_000_000_000_000), 21000, big.NewInt(1_000_000_000), nil)
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"maxFeePerGas", "maxPriorityFeePerGas"} {
		delete(obj, field)
	}
	for field, value := range map[string]string{
		"hash":             signed.Hash().Hex(),
		"from":             crypto.PubkeyToAddress(key.PublicKey).Hex(),
		"blockHash":        string(blockHash),
		"blockNumber":      "0x10",
		"blockTimestamp":   "0x5",
		"transactionIndex": fmt.Sprintf("0x%x", index),
	} {
		encoded, _ := json.Marshal(value)
		obj[field] = encoded
	}
	result, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTransactionInBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "33")
	raw := transactionInBlockFixture(t, blockHash, 0)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	tx, result, err := client.TransactionInBlock(context.Background(), blockHash, 0)
	if err != nil || tx == nil || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionInBlock = tx:%v result:%+v err:%v", tx, result, err)
	}
	viewTx, viewErr := client.EthClient().TransactionInBlock(context.Background(), blockHash, 0)
	if viewErr != nil || viewTx == nil || txHashOf(viewTx) != txHashOf(tx) {
		t.Fatalf("view TransactionInBlock = tx:%v err:%v", viewTx, viewErr)
	}
}

func TestTransactionInBlockNullMapsToNotFound(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "44")
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, nil)
	}
	if _, _, err := client.TransactionInBlock(context.Background(), blockHash, 0); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("TransactionInBlock null error = %v, want ethereum.NotFound", err)
	}
}

func TestTransactionInBlockMultiSourceConflictKeepsEvidence(t *testing.T) {
	client, nodes := fleet(t, 2)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "45")
	nodes[0].Return(methods.MethodGetTransactionByBlockHashAndIndex, transactionInBlockVariant(t, blockHash, 0, 7))
	nodes[1].Return(methods.MethodGetTransactionByBlockHashAndIndex, transactionInBlockVariant(t, blockHash, 0, 8))

	tx, result, err := client.TransactionInBlock(context.Background(), blockHash, 0)
	if tx != nil || !errors.Is(err, ethguard.ErrConflict) || result.Decision == nil {
		t.Fatalf("conflict = tx:%v result:%+v err:%v", tx, result, err)
	}
	if result.Decision.Status != rpc.Unknown || len(result.Decision.Support) != 1 || len(result.Decision.Reject) != 1 {
		t.Fatalf("conflict decision = %+v, want unknown with one support and one reject", result.Decision)
	}
	if result.Decision.Support[0].ID == "" || result.Decision.Reject[0].ID == "" || result.Decision.Support[0].ID == result.Decision.Reject[0].ID {
		t.Fatalf("conflict sources = support:%+v reject:%+v", result.Decision.Support, result.Decision.Reject)
	}
}

func TestTransactionInBlockMultiSourceMalformedFailsWithFaults(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodGetTransactionByBlockHashAndIndex, []byte(`{"not":"a transaction"}`))
	}

	tx, result, err := client.TransactionInBlock(context.Background(), ethapi.Hash("0x"+strings.Repeat("0", 62)+"46"), 0)
	if tx != nil || !errors.Is(err, ethguard.ErrMalformedResponse) || result.Decision == nil {
		t.Fatalf("malformed = tx:%v result:%+v err:%v", tx, result, err)
	}
	if result.Decision.Status != rpc.Unknown || len(result.Decision.Support) != 0 || len(result.Decision.Faults) != 3 {
		t.Fatalf("malformed decision = %+v, want unknown with three faults", result.Decision)
	}
}

func TestTransactionSender(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "55")
	raw := transactionInBlockFixture(t, blockHash, 0)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	tx, txErr := ethapi.NewTransaction(raw)
	if txErr != nil {
		t.Fatal(txErr)
	}
	wantKey, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.PubkeyToAddress(wantKey.PublicKey)
	sender, result, err := client.TransactionSender(context.Background(), &tx, blockHash, 0)
	if err != nil || string(sender) != strings.ToLower(want.Hex()) || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionSender = %s result=%+v err=%v, want %s", sender, result, err, want)
	}
	viewSender, viewErr := client.EthClient().TransactionSender(context.Background(), &tx, blockHash, 0)
	if viewErr != nil || viewSender != sender {
		t.Fatalf("view TransactionSender = %s err=%v, want %s", viewSender, viewErr, sender)
	}
}

func TestTransactionSenderRejectsHashMismatch(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "66")
	raw := transactionInBlockFixture(t, blockHash, 0)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, raw)
	}
	inputRaw := transactionInBlockVariant(t, blockHash, 0, 99)
	input, inputErr := ethapi.NewTransaction(inputRaw)
	if inputErr != nil {
		t.Fatal(inputErr)
	}
	if _, _, err := client.TransactionSender(context.Background(), &input, blockHash, 0); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("hash mismatch error = %v, want malformed response", err)
	}
}

func TestTransactionSenderRejectsMissingFrom(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "77")
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
	var fullTx ethapi.Transaction
	parsed, parseErr := ethapi.NewTransaction(transactionInBlockFixture(t, blockHash, 0))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	fullTx = parsed
	if _, _, err := client.TransactionSender(context.Background(), &fullTx, blockHash, 0); err == nil {
		t.Fatal("missing sender unexpectedly succeeded")
	}
}

func TestTransactionSenderMultiSourceConflictKeepsEvidence(t *testing.T) {
	client, nodes := fleet(t, 2)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "78")
	inputRaw := transactionInBlockVariant(t, blockHash, 0, 7)
	input, inputErr := ethapi.NewTransaction(inputRaw)
	if inputErr != nil {
		t.Fatal(inputErr)
	}
	nodes[0].Return(methods.MethodGetTransactionByBlockHashAndIndex, inputRaw)
	nodes[1].Return(methods.MethodGetTransactionByBlockHashAndIndex, transactionInBlockVariant(t, blockHash, 0, 8))

	sender, result, err := client.TransactionSender(context.Background(), &input, blockHash, 0)
	if sender != "" || !errors.Is(err, ethguard.ErrConflict) || result.Decision == nil {
		t.Fatalf("conflict = sender:%s result:%+v err:%v", sender, result, err)
	}
	if result.Decision.Status != rpc.Unknown || len(result.Decision.Support) != 1 || len(result.Decision.Reject) != 1 {
		t.Fatalf("conflict decision = %+v, want unknown with one support and one reject", result.Decision)
	}
	if result.Decision.Support[0].ID == "" || result.Decision.Reject[0].ID == "" || result.Decision.Support[0].ID == result.Decision.Reject[0].ID {
		t.Fatalf("conflict sources = support:%+v reject:%+v", result.Decision.Support, result.Decision.Reject)
	}
}

func TestTransactionSenderMultiSourceMalformedFailsWithFaults(t *testing.T) {
	client, nodes := fleet(t, 3)
	blockHash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "79")
	inputRaw := transactionInBlockFixture(t, blockHash, 0)
	input, inputErr := ethapi.NewTransaction(inputRaw)
	if inputErr != nil {
		t.Fatal(inputErr)
	}
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodGetTransactionByBlockHashAndIndex, []byte(`{"not":"a transaction"}`))
	}

	sender, result, err := client.TransactionSender(context.Background(), &input, blockHash, 0)
	if sender != "" || !errors.Is(err, ethguard.ErrMalformedResponse) || result.Decision == nil {
		t.Fatalf("malformed = sender:%s result:%+v err:%v", sender, result, err)
	}
	if result.Decision.Status != rpc.Unknown || len(result.Decision.Support) != 0 || len(result.Decision.Faults) != 3 {
		t.Fatalf("malformed decision = %+v, want unknown with three faults", result.Decision)
	}
}

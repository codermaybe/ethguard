package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"math/big"
)

// senderFixtureTransaction builds one fully canonical signed legacy
// transaction information object at the given block index: an independent
// geth-signed payload plus the official inclusion members, spelled for the
// eth_getTransactionByBlockHashAndIndex response shape.
func senderFixtureTransaction(t *testing.T, blockHash ethapi.Hash, index, nonce uint64) json.RawMessage {
	t.Helper()
	key, err := crypto.HexToECDSA("feedbeeffeedbeeffeedbeeffeedbeeffeedbeeffeedbeeffeedbeeffeedbeef")
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
	for field, value := range map[string]string{
		"hash":             signed.Hash().Hex(),
		"from":             crypto.PubkeyToAddress(key.PublicKey).Hex(),
		"blockHash":        string(blockHash),
		"blockNumber":      "0x10",
		"blockTimestamp":   "0x5",
		"transactionIndex": fmt.Sprintf("0x%x", index),
	} {
		encoded, encErr := json.Marshal(value)
		if encErr != nil {
			t.Fatal(encErr)
		}
		obj[field] = encoded
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTransactionSenderRejectsIllegalInputLocally pins S6 on the public
// entrypoints: a nil, zero-selected, multi-selected, unsigned, or
// hash-mismatched caller input is an ErrInvalidRequest rejected before any
// network contact — the root completion result carries no decision and its
// Err equals the trailing error.
func TestTransactionSenderRejectsIllegalInputLocally(t *testing.T) {
	blockHash := ethapi.Hash("0x" + strings.Repeat("22", 32))
	fixture := senderFixtureTransaction(t, blockHash, 1, 0)
	mismatched := senderFixtureTransaction(t, blockHash, 1, 0)
	mismatchedTx, err := ethapi.NewTransaction(mismatched)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedTx.SignedLegacyTransaction.Hash = ethapi.Hash("0x" + strings.Repeat("42", 32))

	unsigned := senderFixtureTransaction(t, blockHash, 1, 0)
	var unsignedObj map[string]json.RawMessage
	if err := json.Unmarshal(unsigned, &unsignedObj); err != nil {
		t.Fatal(err)
	}
	unsignedObj["r"] = json.RawMessage(`"0x0"`)
	unsignedObj["s"] = json.RawMessage(`"0x0"`)
	unsignedRaw, err := json.Marshal(unsignedObj)
	if err != nil {
		t.Fatal(err)
	}
	unsignedTx, err := ethapi.NewTransaction(unsignedRaw)
	if err != nil {
		t.Fatalf("unsigned fixture must pass the schema: %v", err)
	}

	multiSelected := &ethapi.Transaction{SignedLegacyTransaction: mismatchedTx.SignedLegacyTransaction, Signed1559Transaction: &ethapi.EIP1559Transaction{}}

	cases := []struct {
		name string
		tx   *ethapi.Transaction
	}{
		{"zero selected", &ethapi.Transaction{}},
		{"multi selected", multiSelected},
		{"unsigned payload", &unsignedTx},
		{"declared hash mismatch", &mismatchedTx},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, nodes := fleet(t, 2)
			for _, node := range nodes {
				node.Return(methods.MethodGetTransactionByBlockHashAndIndex, fixture)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			sender, result, err := client.TransactionSender(ctx, tc.tx, blockHash, 1)
			if !errors.Is(err, ethguard.ErrInvalidRequest) || sender != "" {
				t.Fatalf("root = %q, %v, want ErrInvalidRequest", sender, err)
			}
			if result.Decision != nil || result.Err != err { //nolint:errorlint // The result must retain the exact error returned to the caller.
				t.Fatalf("root result = %+v, want nil decision with equal Err", result)
			}
			viewSender, viewErr := client.EthClient().TransactionSender(ctx, tc.tx, blockHash, 1)
			if !errors.Is(viewErr, ethguard.ErrInvalidRequest) || viewSender != "" {
				t.Fatalf("view = %q, %v, want ErrInvalidRequest", viewSender, viewErr)
			}
			for _, node := range nodes {
				if calls := node.Calls(); len(calls) != 0 {
					t.Fatalf("illegal input contacted upstream: %+v", calls)
				}
			}
		})
	}
	// nil input keeps its own rejection class on both surfaces.
	client, nodes := fleet(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, result, err := client.TransactionSender(ctx, nil, blockHash, 1); !errors.Is(err, ethguard.ErrInvalidRequest) || result.Decision != nil {
		t.Fatalf("nil input = %v, %+v", err, result)
	}
	if _, err := client.EthClient().TransactionSender(ctx, nil, blockHash, 1); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("view nil input = %v", err)
	}
	for _, node := range nodes {
		if calls := node.Calls(); len(calls) != 0 {
			t.Fatalf("nil input contacted upstream: %+v", calls)
		}
	}
}

// TestTransactionSenderConsumesCanonicalCopy pins S3 on the public
// entrypoints: a caller-constructed union whose declared hash spells the
// correct value in uppercase succeeds — the constructor's canonical copy is
// what the payload bridge consumes — while a genuinely different declared
// hash still fails before any contact, and a case-equivalent input binds the
// remote block/index answer and returns the RPC-declared sender.
func TestTransactionSenderConsumesCanonicalCopy(t *testing.T) {
	blockHash := ethapi.Hash("0x" + strings.Repeat("22", 32))
	fixture := senderFixtureTransaction(t, blockHash, 1, 0)

	var fixtureObj map[string]json.RawMessage
	if err := json.Unmarshal(fixture, &fixtureObj); err != nil {
		t.Fatal(err)
	}
	var declared string
	if err := json.Unmarshal(fixtureObj["hash"], &declared); err != nil {
		t.Fatal(err)
	}
	var from string
	if err := json.Unmarshal(fixtureObj["from"], &from); err != nil {
		t.Fatal(err)
	}

	// Case-equivalent positive: the same payload, the declared hash spelled
	// in uppercase. The canonical copy must make this succeed.
	upper := ethapi.Hash("0X" + strings.ToUpper(strings.TrimPrefix(declared, "0x")))
	upperTx, err := ethapi.NewTransaction(fixture)
	if err != nil {
		t.Fatal(err)
	}
	upperTx.SignedLegacyTransaction.Hash = upper
	if string(upperTx.SignedLegacyTransaction.Hash) == declared {
		t.Fatalf("fixture spelling did not exercise the canonical copy: %q", upperTx.SignedLegacyTransaction.Hash)
	}

	// Genuine mismatch negative: same payload, a different declared hash.
	mismatchTx, err := ethapi.NewTransaction(fixture)
	if err != nil {
		t.Fatal(err)
	}
	mismatchTx.SignedLegacyTransaction.Hash = ethapi.Hash("0x" + strings.Repeat("42", 32))

	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByBlockHashAndIndex, fixture)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sender, result, err := client.TransactionSender(ctx, &upperTx, blockHash, 1)
	if err != nil || sender != ethapi.Address(strings.ToLower(from)) {
		t.Fatalf("canonical-equivalent sender = %q, %v", sender, err)
	}
	if result.Decision == nil {
		t.Fatalf("successful read lost its decision: %+v", result)
	}
	viewSender, viewErr := client.EthClient().TransactionSender(ctx, &upperTx, blockHash, 1)
	if viewErr != nil || viewSender != ethapi.Address(strings.ToLower(from)) {
		t.Fatalf("view canonical-equivalent sender = %q, %v", viewSender, viewErr)
	}

	// Snapshot BEFORE the mismatch call: the assertion is that the local
	// rejection adds zero upstream contact.
	before := make([]int, len(nodes))
	for i, node := range nodes {
		before[i] = len(node.Calls())
	}
	if _, _, err := client.TransactionSender(ctx, &mismatchTx, blockHash, 1); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("mismatched declared hash = %v, want ErrInvalidRequest", err)
	}
	for i, node := range nodes {
		if added := len(node.Calls()) - before[i]; added != 0 {
			t.Fatalf("mismatched hash caused upstream contact: %d new calls on node %d", added, i)
		}
	}
}

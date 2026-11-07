package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func TestTypedRealHeaderRejectsAnotherBlocksContent(t *testing.T) {
	empty := sampleResult(t, "block-empty-hash")
	filled := sampleResult(t, "block-filled-hash")
	var emptyHead, filledHead struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(empty, &emptyHead); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(filled, &filledHead); err != nil {
		t.Fatal(err)
	}
	client, nodes := fleet(t, 1, 1, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, json.RawMessage(empty))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Header-only block reads are a wire form of the generic entry now: the
	// typed facade exposes the full-block read, so the test drives the same
	// wire shape through Call and applies the header projection and hash
	// binding the way the typed layer does.
	decision, err := callDecision(client.Call(ctx, methods.MethodGetBlockByHash, emptyHead.Hash, false))
	var block *types.Block
	if err == nil {
		block, err = ethguard.DecodeBlockHeaderOnly(decision.Value)
	}
	if err != nil {
		t.Fatalf("real empty header: %v", err)
	}
	if decision.Status != rpc.Corroborated || block.Hash() != common.HexToHash(emptyHead.Hash) {
		t.Fatalf("status %v hash %s", decision.Status, block.Hash())
	}

	_, _, typedErr := client.BlockByHash(ctx, common.HexToHash(filledHead.Hash))
	if !errors.Is(typedErr, ethguard.ErrMalformedResponse) {
		t.Fatalf("foreign header err = %v, want ethguard.ErrMalformedResponse", typedErr)
	}
	if errors.Is(typedErr, ethguard.ErrConflict) || errors.Is(typedErr, ethguard.ErrNoValidResponse) || errors.Is(typedErr, ethguard.ErrInvalidRequest) {
		t.Fatalf("foreign header swallowed as %v", typedErr)
	}
}

func TestTypedNullAndEmptyReceiptsStayDistinct(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, nil)
		node.Return(methods.MethodGetTransactionReceipt, nil)
		node.Return(methods.MethodGetTransactionByHash, nil)
		node.Return(methods.MethodGetBlockReceipts, []any{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hash := common.HexToHash("0x" + repeatHex(32))

	// Corroborated absence maps to ethereum.NotFound at the typed boundary;
	// the classified error travels without a decision (clean-value facade).
	if _, _, err := client.BlockByHash(ctx, hash); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("block null = %v, want NotFound", err)
	}
	if _, _, err := client.TransactionReceipt(ctx, hash); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("receipt null = %v, want NotFound", err)
	}
	if _, _, _, err := client.TransactionByHash(ctx, hash); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("tx null = %v, want NotFound", err)
	}
	receipts, _, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(hash, false))
	if err != nil || len(receipts) != 0 {
		t.Fatalf("empty receipts = %v len %d", err, len(receipts))
	}
	if decision, err := callDecision(client.Call(ctx, methods.MethodGetBlockReceipts, hash.Hex())); err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("empty receipts decision = %v status %v, want corroborated", err, decision.Status)
	}
}

func TestTypedReceiptFromAnotherHashIsMalformed(t *testing.T) {
	raw := sampleResult(t, "receipt-one")
	client, nodes := fleet(t, 1, 1, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, json.RawMessage(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+repeatHex(32))); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("err = %v, want ethguard.ErrMalformedResponse", err)
	} else if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) || errors.Is(err, ethereum.NotFound) {
		t.Fatalf("receipt impersonation swallowed as %v", err)
	}
}

func TestTypedLogsNullIsMalformed(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetLogs, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decision, err := callDecision(client.Call(ctx, methods.MethodGetLogs, map[string]any{
		"blockHash": "0x" + repeatHex(32),
	}))
	if !errors.Is(err, ethguard.ErrMalformedResponse) || decision.Status != rpc.Unknown {
		t.Fatalf("logs null = %v status %v", err, decision.Status)
	}
	if errors.Is(err, ethereum.NotFound) || errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("logs null swallowed as %v", err)
	}
}

func sampleResult(t *testing.T, requestID string) json.RawMessage {
	t.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("..", "..", "..", "internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.RequestID != requestID || record.Source.ID != "s1" {
			continue
		}
		_, body, err := replay.BodyOf(record)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Result
	}
	t.Fatalf("missing %s", requestID)
	return nil
}

func repeatHex(n int) string {
	out := make([]byte, n*2)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

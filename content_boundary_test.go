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

	"ethguard"
	"ethguard/internal/rpcsample/replay"
	"ethguard/rpc"
	"ethguard/rpc/methods"
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

	block, decision, err := client.BlockByHash(ctx, common.HexToHash(emptyHead.Hash), false)
	if err != nil {
		t.Fatalf("real empty header: %v", err)
	}
	if decision.Status != rpc.Corroborated || block.Hash() != common.HexToHash(emptyHead.Hash) {
		t.Fatalf("status %v hash %s", decision.Status, block.Hash())
	}

	_, decision, err = client.BlockByHash(ctx, common.HexToHash(filledHead.Hash), false)
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("foreign header err = %v, want ethguard.ErrMalformedResponse", err)
	}
	if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) || errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("foreign header swallowed as %v", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("typed failure status = %v, want Unknown", decision.Status)
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

	_, decision, err := client.BlockByHash(ctx, hash, false)
	if !errors.Is(err, ethereum.NotFound) || decision.Status != rpc.Unknown {
		t.Fatalf("block null = %v status %v", err, decision.Status)
	}
	_, decision, err = client.TransactionReceipt(ctx, hash)
	if !errors.Is(err, ethereum.NotFound) || decision.Status != rpc.Unknown {
		t.Fatalf("receipt null = %v status %v", err, decision.Status)
	}
	_, decision, err = client.TransactionByHash(ctx, hash)
	if !errors.Is(err, ethereum.NotFound) || decision.Status != rpc.Unknown {
		t.Fatalf("tx null = %v status %v", err, decision.Status)
	}
	receipts, decision, err := client.BlockReceipts(ctx, hash)
	if err != nil || len(receipts) != 0 || decision.Status != rpc.Corroborated {
		t.Fatalf("empty receipts = %v len %d status %v", err, len(receipts), decision.Status)
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
	_, decision, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+repeatHex(32)))
	if !errors.Is(err, ethguard.ErrMalformedResponse) || decision.Status != rpc.Unknown {
		t.Fatalf("err = %v status %v", err, decision.Status)
	}
	if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) || errors.Is(err, ethereum.NotFound) {
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
	decision, err := client.Call(ctx, methods.MethodGetLogs, map[string]any{
		"blockHash": "0x" + repeatHex(32),
	})
	if !errors.Is(err, ethguard.ErrMalformedResponse) || decision.Status != rpc.Unknown {
		t.Fatalf("logs null = %v status %v", err, decision.Status)
	}
	if errors.Is(err, ethereum.NotFound) || errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("logs null swallowed as %v", err)
	}
}

func sampleResult(t *testing.T, requestID string) json.RawMessage {
	t.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
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

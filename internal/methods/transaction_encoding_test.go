package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	transactionobject "github.com/codermaybe/ethguard/internal/ethobject/transaction"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
)

func TestTransactionPolicyCanonicalizesValueWithoutSplicingSources(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{txHash(t, raw)}}
	withVendor := injectField(t, raw, "vendorExtension", `"outside"`)
	decision, err := resolveCanonical(t, request, rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
		{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: withVendor}}},
	})
	if err != nil || decision.Decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	want, err := transactionobject.Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decision.Decision.Value, want) {
		t.Fatalf("value = %s, want %s", decision.Decision.Value, want)
	}
	if bytes.Contains(decision.Decision.Value, []byte("vendorExtension")) {
		t.Fatalf("vendor field survived: %s", decision.Decision.Value)
	}
}

func TestTransactionMethodsAcceptOfficialPayloadTypes(t *testing.T) {
	covered := make(map[string]bool)
	err := fs.WalkDir(spec.VectorsFS(), "vectors/eth_getTransactionByHash", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(spec.VectorsFS(), path)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			if len(vector.Error) > 0 || bytes.Equal(bytes.TrimSpace(vector.Result), []byte("null")) {
				continue
			}
			t.Run(vector.Identity(), func(t *testing.T) {
				var fields struct {
					Type      string `json:"type"`
					Hash      string `json:"hash"`
					BlockHash string `json:"blockHash"`
					Index     string `json:"transactionIndex"`
				}
				if err := json.Unmarshal(vector.Result, &fields); err != nil {
					t.Fatal(err)
				}
				covered[fields.Type] = true
				request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{fields.Hash}}
				if _, err := processTransactionByHash(context.Background(), request, vector.Result, nil); err != nil {
					t.Fatalf("official by-hash type %s rejected: %v", fields.Type, err)
				}
				byHash, err := normalizeTransactionByHash(vector.Result)
				if err != nil {
					t.Fatal(err)
				}
				request = rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{fields.BlockHash, fields.Index}}
				if _, err := processTransactionInBlock(context.Background(), request, vector.Result, nil); err != nil {
					t.Fatalf("official block/index type %s rejected: %v", fields.Type, err)
				}
				byBlock, err := normalizeTransactionInBlock(vector.Result)
				if err != nil || byBlock != byHash {
					t.Fatalf("method outputs differ: by-hash=%s by-block=%s err=%v", byHash, byBlock, err)
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, transactionType := range []string{"0x0", "0x1", "0x2", "0x3", "0x4"} {
		if !covered[transactionType] {
			t.Errorf("official payload type %s was not exercised", transactionType)
		}
	}
}

func TestTransactionPendingContextIsAnEntireConsistentState(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	pending := raw
	for _, field := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		pending = injectField(t, pending, field, `null`)
	}
	request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{txHash(t, raw)}}
	if _, err := processTransactionByHash(context.Background(), request, pending, nil); err != nil {
		t.Fatalf("pending admission = %v", err)
	}
	key, err := normalizeTransactionByHash(pending)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		if string(fields[field]) != "null" {
			t.Fatalf("pending %s = %s", field, fields[field])
		}
		if field == "blockTimestamp" {
			continue
		}
		if _, err := processTransactionByHash(context.Background(), request, deleteField(t, pending, field), nil); !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("missing pending %s admission = %v", field, err)
		}
	}
	mixed := injectField(t, raw, "blockHash", `null`)
	if _, err := processTransactionByHash(context.Background(), request, mixed, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("mixed inclusion state admission = %v", err)
	}
	var mined struct {
		BlockHash string `json:"blockHash"`
		Index     string `json:"transactionIndex"`
	}
	if err := json.Unmarshal(raw, &mined); err != nil {
		t.Fatal(err)
	}
	byBlock := rpc.Request{Method: MethodGetTransactionByBlockHashAndIndex, Params: []any{mined.BlockHash, mined.Index}}
	if _, err := normalizeTransactionInBlock(pending); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending transaction contributed a block/index comparison key: %v", err)
	}
	if _, err := processTransactionInBlock(context.Background(), byBlock, pending, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending transaction accepted by block/index: %v", err)
	}
	if _, err := processTransactionInBlock(context.Background(), byBlock, foldHex(t, raw), nil); err != nil {
		t.Fatalf("mined case compatibility rejected by block/index: %v", err)
	}
}

package gethcompat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codermaybe/ethguard/gethcompat"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestDialSimulateUsesOfficialTypesAndWireMethod(t *testing.T) {
	var request struct {
		JSONRPC string            `json:"jsonrpc"`
		Method  string            `json:"method"`
		Params  []json.RawMessage `json:"params"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[{"number":"0x1","hash":"0x0000000000000000000000000000000000000000000000000000000000000001","timestamp":"0x2","gasLimit":"0x3","gasUsed":"0x4","miner":"0x0000000000000000000000000000000000000001","calls":[{"returnData":"0x","logs":[],"gasUsed":"0x1","maxUsedGas":"0x1","status":"0x1"}]}]}`))
	}))
	t.Cleanup(srv.Close)

	client, err := gethcompat.DialSimulate(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DialSimulate: %v", err)
	}
	t.Cleanup(client.Close)

	results, err := client.SimulateV1(context.Background(), ethclient.SimulateOptions{
		BlockStateCalls: []ethclient.SimulateBlock{{Calls: []ethereum.CallMsg{}}},
	}, nil)
	if err != nil {
		t.Fatalf("SimulateV1: %v", err)
	}
	if request.JSONRPC != "2.0" || request.Method != "eth_simulateV1" {
		t.Fatalf("request = %+v, want JSON-RPC eth_simulateV1", request)
	}
	if len(request.Params) != 2 || len(results) != 1 || len(results[0].Calls) != 1 {
		t.Fatalf("params/results = %d/%+v, want two params and one call", len(request.Params), results)
	}
	if results[0].Number == nil || results[0].Number.Uint64() != 1 || results[0].Calls[0].Status != 1 {
		t.Fatalf("decoded result = %+v", results[0])
	}
}

func TestWrapSimulateRejectsNil(t *testing.T) {
	if _, err := gethcompat.WrapSimulate(nil); err == nil {
		t.Fatal("WrapSimulate(nil) succeeded")
	}
}

func TestSimulateClientSignatureIsOfficial(t *testing.T) {
	var _ interface {
		SimulateV1(context.Context, ethclient.SimulateOptions, *gethrpc.BlockNumberOrHash) ([]ethclient.SimulateBlockResult, error)
	} = (*gethcompat.SimulateClient)(nil)
}

package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func TestEstimateGasNativeHTTPAndRawWSSWireAndQuantity(t *testing.T) {
	rawBlock := fixtureBlock(t, 16)
	var block struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(rawBlock, &block); err != nil || block.Hash == "" {
		t.Fatalf("anchor fixture = %s err=%v", rawBlock, err)
	}
	var records [2]atomic.Value
	answers := []string{`"0x5208"`, `"0x5308"`}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, estimateGasTransportAnswer(rawBlock, answers[0], &records[0])),
		newSourceWirePeer(t, true, estimateGasTransportAnswer(rawBlock, answers[1], &records[1])),
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

	from := common.HexToAddress("0x1111")
	to := common.HexToAddress("0x2222")
	msg := ethereum.CallMsg{
		From:     from,
		To:       &to,
		Data:     []byte{0xde, 0xad},
		Value:    big.NewInt(42),
		Gas:      21_000,
		GasPrice: big.NewInt(3),
	}
	gas, result, err := client.EstimateGas(ctx, msg)
	if err != nil || result.Decision == nil || result.Decision.Status != rpc.Selected || gas != 0x5208 {
		t.Fatalf("EstimateGas result=%+v gas=%d err=%v, want lower-median Selected 0x5208", result, gas, err)
	}
	if peers[0].httpConnections.Load() == 0 || peers[1].connections.Load() == 0 {
		t.Fatalf("transport connections = http:%d wss:%d", peers[0].httpConnections.Load(), peers[1].connections.Load())
	}
	for i, slot := range records {
		assertEstimateGasWire(t, slot.Load(), block.Hash, msg, i)
	}
}

func TestEstimateGasNativeHTTPAndRawWSSMalformedAndAllError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     string
		rpcError   bool
		wantError  error
		wantStatus rpc.Status
		wantValue  bool
	}{
		{name: "null", result: "null", wantError: ethguard.ErrNoValidResponse, wantStatus: rpc.Unknown},
		{name: "overflow", result: `"0x10000000000000000"`, wantError: ethguard.ErrMalformedResponse, wantStatus: rpc.Selected, wantValue: true},
		{name: "all provider errors", result: `"error":{"code":-32000,"message":"execution reverted"}`, rpcError: true, wantError: ethguard.ErrNoValidResponse, wantStatus: rpc.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawBlock := fixtureBlock(t, 16)
			var block struct {
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(rawBlock, &block); err != nil || block.Hash == "" {
				t.Fatalf("anchor fixture = %s err=%v", rawBlock, err)
			}
			var records [2]atomic.Value
			answer := func(slot *atomic.Value) func(sourceWireRequest) string {
				return func(req sourceWireRequest) string {
					if req.Method == methods.MethodGetBlockByNumber {
						return `"result":` + string(rawBlock)
					}
					if req.Method == methods.MethodEstimateGas {
						slot.Store(req)
						if tc.rpcError {
							return tc.result
						}
						return `"result":` + tc.result
					}
					return `"result":"0x1"`
				}
			}
			peers := []*sourceWirePeer{
				newSourceWirePeer(t, false, answer(&records[0])),
				newSourceWirePeer(t, true, answer(&records[1])),
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
			to := common.HexToAddress("0x2222")
			msg := ethereum.CallMsg{To: &to, Data: []byte{0xde, 0xad}}
			gas, result, callErr := client.EstimateGas(ctx, msg)
			if gas != 0 || !errors.Is(callErr, tc.wantError) || result.Decision == nil || result.Decision.Status != tc.wantStatus || (result.Decision.Value != nil) != tc.wantValue {
				t.Fatalf("EstimateGas gas=%d result=%+v err=%v, want status=%s value=%v err=%v", gas, result, callErr, tc.wantStatus, tc.wantValue, tc.wantError)
			}
			for i, slot := range records {
				assertEstimateGasWire(t, slot.Load(), block.Hash, msg, i)
			}
		})
	}
}

func estimateGasTransportAnswer(rawBlock []byte, result string, slot *atomic.Value) func(sourceWireRequest) string {
	return func(req sourceWireRequest) string {
		if req.Method == methods.MethodGetBlockByNumber {
			return `"result":` + string(rawBlock)
		}
		if req.Method == methods.MethodEstimateGas {
			slot.Store(req)
			return `"result":` + result
		}
		return `"result":"0x1"`
	}
}

func assertEstimateGasWire(t *testing.T, loaded any, blockHash string, msg ethereum.CallMsg, source int) {
	t.Helper()
	if loaded == nil {
		t.Fatalf("source %d did not record eth_estimateGas", source)
	}
	req := loaded.(sourceWireRequest)
	if req.Method != methods.MethodEstimateGas || len(req.ID) == 0 || len(req.Params) != 2 {
		t.Fatalf("source %d request=%+v, want eth_estimateGas call object + pinned ref", source, req)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(req.Params[0], &object); err != nil {
		t.Fatalf("source %d call object: %v", source, err)
	}
	var from, to, input, value, gas, gasPrice, ref string
	for key, dst := range map[string]*string{"from": &from, "to": &to, "input": &input} {
		if err := json.Unmarshal(object[key], dst); err != nil {
			t.Fatalf("source %d %s=%s: %v", source, key, object[key], err)
		}
	}
	for key, dst := range map[string]*string{"value": &value, "gas": &gas, "gasPrice": &gasPrice} {
		if raw, ok := object[key]; ok {
			if err := json.Unmarshal(raw, dst); err != nil {
				t.Fatalf("source %d %s=%s: %v", source, key, raw, err)
			}
		}
	}
	if err := json.Unmarshal(req.Params[1], &ref); err != nil {
		t.Fatalf("source %d block ref=%s: %v", source, req.Params[1], err)
	}
	if from != msg.From.Hex() || to != msg.To.Hex() || input != "0xdead" || ref != blockHash {
		t.Fatalf("source %d wire object/ref = from:%q to:%q input:%q value:%q gas:%q gasPrice:%q ref:%q, want pinned %q", source, from, to, input, value, gas, gasPrice, ref, blockHash)
	}
	if msg.Value != nil && value != "0x2a" || msg.Gas != 0 && gas != "0x5208" || msg.GasPrice != nil && gasPrice != "0x3" {
		t.Fatalf("source %d optional call fields = value:%q gas:%q gasPrice:%q", source, value, gas, gasPrice)
	}
	if _, ok := object["data"]; ok {
		t.Fatalf("source %d call object used data instead of input: %s", source, req.Params[0])
	}
}

package ethadapter_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/rpctest"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestIndependentEarliestFilterQueryMapsLockedGethTagToGenesis(t *testing.T) {
	query := ethereum.FilterQuery{FromBlock: big.NewInt(int64(gethrpc.EarliestBlockNumber)), ToBlock: big.NewInt(int64(gethrpc.EarliestBlockNumber))}
	got, _, err := ethadapter.FromGethFilterQuery(query)
	if err != nil {
		t.Fatalf("legal geth earliest tag was rejected: %v", err)
	}
	fields := contractFields(t, got)
	if string(fields["fromBlock"]) != `"0x0"` || string(fields["toBlock"]) != `"0x0"` {
		t.Fatalf("earliest did not normalize to core genesis: %s", contractJSON(t, got))
	}
}

func TestIndependentEarliestPublicHTTPConsumersReachNativeWire(t *testing.T) {
	for _, method := range []string{"FilterLogs", "BlockReceipts", "SimulateV1"} {
		t.Run(method, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			head := contractBaseHeader()
			head.Number = new(big.Int)
			contractInstallBlock(t, node, head, nil, nil)
			node.Return("eth_getLogs", []any{})
			node.Return("eth_getBlockReceipts", []any{})
			client := contractClient(t, node)
			ref := gethrpc.BlockNumberOrHashWithNumber(gethrpc.EarliestBlockNumber)
			wireMethod := ""
			switch method {
			case "FilterLogs":
				wireMethod = "eth_getLogs"
				rows, err := client.FilterLogs(t.Context(), ethereum.FilterQuery{FromBlock: big.NewInt(int64(gethrpc.EarliestBlockNumber)), ToBlock: big.NewInt(int64(gethrpc.EarliestBlockNumber))})
				if err != nil || len(rows) != 0 {
					t.Fatalf("earliest FilterLogs=%v %v", rows, err)
				}
			case "BlockReceipts":
				wireMethod = "eth_getBlockReceipts"
				rows, err := client.BlockReceipts(t.Context(), ref)
				if err != nil || len(rows) != 0 {
					t.Fatalf("earliest BlockReceipts=%v %v", rows, err)
				}
			case "SimulateV1":
				wireMethod = "eth_simulateV1"
				raw := contractOfficialResult(t, "eth_simulateV1/ethSimulate-simple.io")
				var want []ethclient.SimulateBlockResult
				if err := json.Unmarshal(raw, &want); err != nil {
					t.Fatal(err)
				}
				node.ReturnRaw(wireMethod, raw)
				opts := ethclient.SimulateOptions{BlockStateCalls: []ethclient.SimulateBlock{{Calls: make([]ethereum.CallMsg, len(want[0].Calls))}}}
				got, err := client.SimulateV1(t.Context(), opts, &ref)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("earliest SimulateV1=%#v %v", got, err)
				}
			}
			calls := 0
			for _, call := range node.Calls() {
				if call.Method != wireMethod {
					continue
				}
				calls++
				if method == "FilterLogs" {
					var query map[string]json.RawMessage
					if len(call.Params) != 1 || json.Unmarshal(call.Params[0], &query) != nil || string(query["fromBlock"]) != `"0x0"` || string(query["toBlock"]) != `"0x0"` {
						t.Fatalf("earliest filter wire changed: %+v", call)
					}
				} else if len(call.Params) == 0 || string(call.Params[len(call.Params)-1]) != `"0x0"` {
					t.Fatalf("earliest reference wire changed: %+v", call)
				}
			}
			if calls != 1 {
				t.Fatalf("business calls=%d want 1", calls)
			}
		})
	}
}

func TestIndependentEarliestSubscriptionQueryReachesNativeFilter(t *testing.T) {
	block := bridgeFixture(t, common.Hash{}, 17, 1, 1, 1)
	peer := newBridgePeer(t, block)
	client := bridgeClient(t, peer)
	out := make(chan types.Log)
	sub, err := client.SubscribeFilterLogs(t.Context(), ethereum.FilterQuery{FromBlock: big.NewInt(int64(gethrpc.EarliestBlockNumber))}, out)
	if err != nil {
		t.Fatalf("legal earliest subscription bound was rejected: %v", err)
	}
	defer sub.Unsubscribe()
	peer.push(t, block)
	if got := bridgeAwait(t, out); !reflect.DeepEqual(got, block.logs[0]) {
		t.Fatal("earliest subscription bound changed delivered log")
	}
	bridgeWaitClose(t, sub.Unsubscribe)
	bridgeTerminal(t, sub, nil)
}

func TestIndependentUnknownNegativeFilterTagsStillFailBeforeNetwork(t *testing.T) {
	outsideInt64 := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 64))
	outsideInt64.Sub(outsideInt64, big.NewInt(5)) // Int64 truncation would alias earliest.
	for _, number := range []*big.Int{big.NewInt(-6), outsideInt64} {
		t.Run(number.String(), func(t *testing.T) {
			query := ethereum.FilterQuery{FromBlock: number}
			if _, _, err := ethadapter.FromGethFilterQuery(query); err == nil {
				t.Fatal("unknown negative filter tag accepted by pure conversion")
			}
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			client := contractClient(t, node)
			_, err := client.FilterLogs(t.Context(), query)
			var conversion *ethadapter.ConversionError
			if !errors.As(err, &conversion) {
				t.Fatalf("invalid negative tag must remain a local conversion error: %v", err)
			}
			if len(node.Calls()) != 0 {
				t.Fatal("unknown negative block tag reached upstream")
			}
		})
	}
}

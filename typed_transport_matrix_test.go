package ethguard_test

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// TestTypedTransportMatrix exercises the same typed reads through native HTTP
// and the raw WSS adapter. sourceWirePeer is deliberately shared with the
// mixed-source tests, so this closes the transport-to-execution seam without
// adding a second server or a subscription fixture.
func TestTypedTransportMatrix(t *testing.T) {
	rawBlock := fixtureBlock(t, 17)
	receiptHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	rawReceipt := fixtureReceipt(t, receiptHash, 17)
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			if req.Method == methods.MethodGetTransactionReceipt {
				return `"result":` + string(rawReceipt)
			}
			if req.Method == methods.MethodGetBlockReceipts {
				return `"result":[` + string(rawReceipt) + `]`
			}
			return `"result":"0x1"`
		}),
		newSourceWirePeer(t, true, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			if req.Method == methods.MethodGetTransactionReceipt {
				return `"result":` + string(rawReceipt)
			}
			if req.Method == methods.MethodGetBlockReceipts {
				return `"result":[` + string(rawReceipt) + `]`
			}
			return `"result":"0x1"`
		}),
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

	id, _, err := client.ChainID(ctx)
	if err != nil || id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ChainID = %v, err=%v", id, err)
	}
	header, _, err := client.HeaderByNumber(ctx, big.NewInt(17))
	if err != nil || header == nil || header.Number.Uint64() != 17 {
		t.Fatalf("HeaderByNumber = %v, err=%v", header, err)
	}
	receipt, _, err := client.TransactionReceipt(ctx, receiptHash)
	if err != nil || receipt == nil || receipt.TxHash != receiptHash {
		t.Fatalf("TransactionReceipt = %v, err=%v", receipt, err)
	}
	receipts, _, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(receipt.BlockHash, false))
	if err != nil || len(receipts) != 1 || receipts[0].TxHash != receiptHash {
		t.Fatalf("BlockReceipts = %v, err=%v", receipts, err)
	}
	if peers[0].httpConnections.Load() == 0 || peers[1].connections.Load() == 0 {
		t.Fatalf("transport connections = http:%d wss:%d", peers[0].httpConnections.Load(), peers[1].connections.Load())
	}
	// Both transports must have received the identity probe and the typed
	// header request. Track those methods independently from the aggregate
	// request total, so a connection or unrelated request cannot satisfy this
	// seam test accidentally. The WSS peer uses the same JSON-RPC request bytes
	// as HTTP; its frame lifecycle is covered by internal/transport/wss tests.
	for i, peer := range peers {
		if peer.chainIDRequests.Load() == 0 || peer.headerRequests.Load() == 0 {
			t.Fatalf("peer %d method counts = chainID:%d header:%d total:%d, want identity + header", i, peer.chainIDRequests.Load(), peer.headerRequests.Load(), peer.requests.Load())
		}
	}
}

// TestTypedCallContractTransportFixture drives one captured eth_call result
// through native HTTP and raw WSS. The call starts at latest, so both sources
// must first establish the same head and then receive the pinned hash. The
// captured call object/result is loaded from the checked-in replay fixture;
// only the anchor block is the existing self-consistent local block fixture.
func TestTypedCallContractTransportFixture(t *testing.T) {
	_, records, err := replay.LoadBatch("internal/rpcsample/testdata/eip1898-object/2026-09-23")
	if err != nil {
		t.Fatal(err)
	}
	var capturedRequest, capturedResult json.RawMessage
	for _, record := range records {
		if record.RequestID != "call-hash-object" || record.Source.ID != "s2" {
			continue
		}
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(record.Request), &request); err != nil {
			t.Fatal(err)
		}
		if request.Method != methods.MethodCall || len(request.Params) != 2 {
			t.Fatalf("captured request = %+v", request)
		}
		isError, body, err := replay.BodyOf(record)
		if err != nil || isError {
			t.Fatalf("captured response = error:%v err:%v", isError, err)
		}
		var response struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &response); err != nil || len(response.Result) == 0 {
			t.Fatalf("captured response body = %s err=%v", body, err)
		}
		capturedRequest, capturedResult = request.Params[0], response.Result
		break
	}
	if len(capturedRequest) == 0 || len(capturedResult) == 0 {
		t.Fatal("call-hash-object/s2 fixture record not found")
	}
	var capturedObject map[string]json.RawMessage
	if err := json.Unmarshal(capturedRequest, &capturedObject); err != nil {
		t.Fatal(err)
	}
	var toText, dataText string
	if err := json.Unmarshal(capturedObject["to"], &toText); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(capturedObject["data"], &dataText); err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress(toText)
	data, err := hexutil.Decode(dataText)
	if err != nil {
		t.Fatal(err)
	}
	var resultText string
	if err := json.Unmarshal(capturedResult, &resultText); err != nil {
		t.Fatal(err)
	}
	wantData, err := hexutil.Decode(resultText)
	if err != nil {
		t.Fatal(err)
	}
	rawBlock := fixtureBlock(t, 17)
	var block struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(rawBlock, &block); err != nil || block.Hash == "" {
		t.Fatalf("anchor fixture = %s err=%v", rawBlock, err)
	}
	var httpCall, wssCall atomic.Value
	answer := func(slot *atomic.Value) func(sourceWireRequest) string {
		return func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			if req.Method == methods.MethodCall {
				slot.Store(req)
				return `"result":` + string(capturedResult)
			}
			return `"result":"0x1"`
		}
	}
	httpPeer := newSourceWirePeer(t, false, answer(&httpCall))
	wssPeer := newSourceWirePeer(t, true, answer(&wssCall))
	client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
		{ID: "http", Endpoint: httpPeer.endpoint},
		{ID: "wss", Endpoint: wssPeer.endpoint, Transport: config.TransportWSS},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, result, err := client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("CallContract result=%+v data=%x err=%v", result, got, err)
	}
	if string(got) != string(wantData) {
		t.Fatalf("result = 0x%x, want fixture 0x%x", got, wantData)
	}
	for _, source := range []struct {
		name string
		slot *atomic.Value
	}{
		{name: "http", slot: &httpCall},
		{name: "wss", slot: &wssCall},
	} {
		loaded := source.slot.Load()
		if loaded == nil {
			t.Fatalf("%s did not receive eth_call", source.name)
		}
		req := loaded.(sourceWireRequest)
		if len(req.ID) == 0 || len(req.Params) != 2 {
			t.Fatalf("%s request = %+v", source.name, req)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(req.Params[0], &object); err != nil {
			t.Fatalf("%s call object: %v", source.name, err)
		}
		for _, key := range []string{"from", "to", "input"} {
			if _, ok := object[key]; !ok {
				t.Fatalf("%s call object missing %q: %s", source.name, key, req.Params[0])
			}
		}
		for _, key := range []string{"data", "value", "gas", "gasPrice", "maxFeePerGas", "maxPriorityFeePerGas", "accessList", "maxFeePerBlobGas", "blobVersionedHashes", "authorizationList"} {
			if _, ok := object[key]; ok {
				t.Fatalf("%s call object unexpectedly includes %q: %s", source.name, key, req.Params[0])
			}
		}
		var from, toWire, input, ref string
		_ = json.Unmarshal(object["from"], &from)
		_ = json.Unmarshal(object["to"], &toWire)
		_ = json.Unmarshal(object["input"], &input)
		_ = json.Unmarshal(req.Params[1], &ref)
		if from != "0x0000000000000000000000000000000000000000" || toWire != strings.ToLower(to.Hex()) || input != hexutil.Encode(data) || ref != block.Hash {
			t.Fatalf("%s wire object/ref = from:%q to:%q input:%q ref:%q, want pinned %q", source.name, from, toWire, input, ref, block.Hash)
		}
	}
}

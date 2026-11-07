package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/ethereum/go-ethereum/core/types"
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

// TestTypedTransactionByHashTransportFixture drives the same signed
// transaction through native HTTP and raw WSS.  The transaction hash and
// inclusion context are decoded identically on both transports, while the
// request must carry exactly one hash parameter.
func TestTypedTransactionByHashTransportFixture(t *testing.T) {
	rawTx := signedTxFixture(t, true)
	var wireTx struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(rawTx, &wireTx); err != nil {
		t.Fatal(err)
	}
	txHash := common.HexToHash(wireTx.Hash)
	var requests [2]atomic.Value
	answer := func(slot *atomic.Value) func(sourceWireRequest) string {
		return func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetTransactionByHash {
				slot.Store(req)
				return `"result":` + string(rawTx)
			}
			return `"result":"0x1"`
		}
	}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, answer(&requests[0])),
		newSourceWirePeer(t, true, answer(&requests[1])),
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
	got, pending, result, err := client.TransactionByHash(ctx, txHash)
	if err != nil || pending || got == nil || got.Hash() != txHash {
		t.Fatalf("TransactionByHash = tx:%v pending:%v result:%+v err:%v", got, pending, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v, want corroborated", result.Decision)
	}
	for i, slot := range requests {
		loaded := slot.Load()
		if loaded == nil {
			t.Fatalf("transport %d did not receive eth_getTransactionByHash", i)
		}
		req := loaded.(sourceWireRequest)
		if len(req.Params) != 1 || wireString(t, req.Params[0]) != txHash.Hex() {
			t.Fatalf("transport %d request = %+v, want one hash parameter %q", i, req, txHash.Hex())
		}
	}
}

// TestTypedTransactionByHashTransportNullParity locks the method-specific
// null rule through both transport paths: a corroborated JSON null is
// ethereum.NotFound, rather than a malformed transaction or an empty value.
func TestTypedTransactionByHashTransportNullParity(t *testing.T) {
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetTransactionByHash {
				return `"result":null`
			}
			return `"result":"0x1"`
		}),
		newSourceWirePeer(t, true, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetTransactionByHash {
				return `"result":null`
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
	_, _, result, err := client.TransactionByHash(ctx, common.HexToHash("0x"+strings.Repeat("42", 32)))
	if !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("TransactionByHash null err = %v, want ethereum.NotFound", err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v, want corroborated null", result.Decision)
	}
}

// TestTypedTransactionCompatibilityTransportMatrix drives the three
// block-addressed transaction methods through both native HTTP and raw WSS.
// These methods do not establish a moving reference, so the fixture isolates
// request encoding, inclusion binding, and sender projection at the transport
// boundary.
func TestTypedTransactionCompatibilityTransportMatrix(t *testing.T) {
	blockHash := common.HexToHash("0x" + strings.Repeat("33", 32))
	rawTx := transactionInBlockFixture(t, blockHash, 0)
	var tx types.Transaction
	if err := json.Unmarshal(rawTx, &tx); err != nil {
		t.Fatal(err)
	}
	type evidence struct {
		count   atomic.Value
		inBlock atomic.Value
		sender  atomic.Value
	}
	var records [2]evidence
	answer := func(record *evidence) func(sourceWireRequest) string {
		return func(req sourceWireRequest) string {
			switch req.Method {
			case methods.MethodGetBlockTransactionCountByHash:
				record.count.Store(req)
				return `"result":"0x2"`
			case methods.MethodGetTransactionByBlockHashAndIndex:
				record.inBlock.Store(req)
				record.sender.Store(req)
				return `"result":` + string(rawTx)
			default:
				return `"result":"0x1"`
			}
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

	count, countResult, err := client.TransactionCount(ctx, blockHash)
	if err != nil || count != 2 || countResult.Decision == nil || countResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionCount = %d result=%+v err=%v", count, countResult, err)
	}
	got, inBlockResult, err := client.TransactionInBlock(ctx, blockHash, 0)
	if err != nil || got == nil || got.Hash() != tx.Hash() || inBlockResult.Decision == nil || inBlockResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionInBlock = %v result=%+v err=%v", got, inBlockResult, err)
	}
	sender, senderResult, err := client.TransactionSender(ctx, &tx, blockHash, 0)
	if err != nil || sender == (common.Address{}) || senderResult.Decision == nil || senderResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionSender = %s result=%+v err=%v", sender, senderResult, err)
	}

	for i, record := range records {
		loaded := record.count.Load()
		if loaded == nil {
			t.Fatalf("source %d did not receive transaction count", i)
		}
		countReq := loaded.(sourceWireRequest)
		if len(countReq.Params) != 1 || wireString(t, countReq.Params[0]) != blockHash.Hex() {
			t.Fatalf("source %d transaction count params=%s", i, countReq.Params)
		}
		for name, slot := range map[string]*atomic.Value{
			"in-block": &record.inBlock,
			"sender":   &record.sender,
		} {
			loaded = slot.Load()
			if loaded == nil {
				t.Fatalf("source %d did not receive %s request", i, name)
			}
			req := loaded.(sourceWireRequest)
			if len(req.Params) != 2 || wireString(t, req.Params[0]) != blockHash.Hex() || wireString(t, req.Params[1]) != "0x0" {
				t.Fatalf("source %d %s params=%s", i, name, req.Params)
			}
		}
	}
}

// TestTypedTransactionCompatibilityNullParity keeps the protocol null rule
// identical across native HTTP and raw WSS for the block-addressed transaction
// family. A corroborated null is a typed NotFound result, not a malformed
// response or an empty transaction value.
func TestTypedTransactionCompatibilityNullParity(t *testing.T) {
	blockHash := common.HexToHash("0x" + strings.Repeat("44", 32))
	rawTx := transactionInBlockFixture(t, blockHash, 0)
	var tx types.Transaction
	if err := json.Unmarshal(rawTx, &tx); err != nil {
		t.Fatal(err)
	}
	answer := func(req sourceWireRequest) string {
		switch req.Method {
		case methods.MethodGetBlockTransactionCountByHash,
			methods.MethodGetTransactionByBlockHashAndIndex:
			return `"result":null`
		default:
			return `"result":"0x1"`
		}
	}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, answer),
		newSourceWirePeer(t, true, answer),
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

	count, countResult, countErr := client.TransactionCount(ctx, blockHash)
	if count != 0 || !errors.Is(countErr, ethereum.NotFound) || countResult.Decision == nil || countResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionCount null = %d result=%+v err=%v", count, countResult, countErr)
	}
	inBlock, inBlockResult, inBlockErr := client.TransactionInBlock(ctx, blockHash, 0)
	if inBlock != nil || !errors.Is(inBlockErr, ethereum.NotFound) || inBlockResult.Decision == nil || inBlockResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionInBlock null = %v result=%+v err=%v", inBlock, inBlockResult, inBlockErr)
	}
	sender, senderResult, senderErr := client.TransactionSender(ctx, &tx, blockHash, 0)
	if sender != (common.Address{}) || !errors.Is(senderErr, ethereum.NotFound) || senderResult.Decision == nil || senderResult.Decision.Status != rpc.Corroborated {
		t.Fatalf("TransactionSender null = %s result=%+v err=%v", sender, senderResult, senderErr)
	}
}

// TestTypedFeeHistoryTransportFixture checks the typed projection and exact
// parameter order on native HTTP and raw WSS.  An explicit block number keeps
// this transport seam independent of the moving-head anchor path.
func TestTypedFeeHistoryTransportFixture(t *testing.T) {
	var requests [2]atomic.Value
	const rawHistory = `{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.5,0.25],"reward":[["0x3"],["0x4"]]}`
	answer := func(slot *atomic.Value) func(sourceWireRequest) string {
		return func(req sourceWireRequest) string {
			if req.Method == methods.MethodFeeHistory {
				slot.Store(req)
				return `"result":` + rawHistory
			}
			return `"result":"0x1"`
		}
	}
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, answer(&requests[0])),
		newSourceWirePeer(t, true, answer(&requests[1])),
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
	history, result, err := client.FeeHistory(ctx, 2, big.NewInt(16), []float64{50})
	if err != nil || history == nil || history.OldestBlock.Int64() != 16 || len(history.BaseFee) != 3 || len(history.Reward) != 2 || history.Reward[0][0].Int64() != 3 {
		t.Fatalf("FeeHistory = %+v result=%+v err=%v", history, result, err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v, want corroborated", result.Decision)
	}
	for i, slot := range requests {
		loaded := slot.Load()
		if loaded == nil {
			t.Fatalf("transport %d did not receive eth_feeHistory", i)
		}
		req := loaded.(sourceWireRequest)
		if len(req.Params) != 3 || wireString(t, req.Params[1]) != "0x10" {
			t.Fatalf("transport %d params = %s, want [count, 0x10, percentiles]", i, req.Params)
		}
		var count uint64
		if err := json.Unmarshal(req.Params[0], &count); err != nil || count != 2 {
			t.Fatalf("transport %d block count = %s", i, req.Params[0])
		}
		var percentiles []float64
		if err := json.Unmarshal(req.Params[2], &percentiles); err != nil || len(percentiles) != 1 || percentiles[0] != 50 {
			t.Fatalf("transport %d percentiles = %s", i, req.Params[2])
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

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
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func TestTypedFilterLogsNativeHTTPAndRawWSS(t *testing.T) {
	rawBlock := fixtureBlock(t, 16)
	blockHash := blockHashFromFixture(t, rawBlock)
	rawLogs := transportLogs(t, blockHash, "0x10")

	var records [2]atomic.Value
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, filterLogsTransportAnswer(rawBlock, rawLogs, &records[0])),
		newSourceWirePeer(t, true, filterLogsTransportAnswer(rawBlock, rawLogs, &records[1])),
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

	address := common.HexToAddress("0x2")
	topic := common.HexToHash("0x3")
	logs, result, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(0),
		Addresses: []common.Address{address},
		Topics:    [][]common.Hash{{topic}},
	})
	if err != nil || result.Decision == nil || result.Decision.Status != rpc.Corroborated {
		t.Fatalf("FilterLogs result=%+v logs=%+v err=%v", result, logs, err)
	}
	if len(logs) != 1 || logs[0].BlockNumber != 16 || logs[0].BlockHash != common.HexToHash(blockHash) {
		t.Fatalf("FilterLogs logs=%+v", logs)
	}
	if peers[0].httpConnections.Load() == 0 || peers[1].connections.Load() == 0 {
		t.Fatalf("transport connections = http:%d wss:%d", peers[0].httpConnections.Load(), peers[1].connections.Load())
	}

	for i, slot := range records {
		loaded := slot.Load()
		if loaded == nil {
			t.Fatalf("source %d did not receive eth_getLogs", i)
		}
		req := loaded.(sourceWireRequest)
		if req.Method != methods.MethodGetLogs || len(req.Params) != 1 {
			t.Fatalf("source %d request=%+v, want one eth_getLogs filter", i, req)
		}
		var filter map[string]json.RawMessage
		if err := json.Unmarshal(req.Params[0], &filter); err != nil {
			t.Fatalf("source %d filter: %v", i, err)
		}
		var from, to string
		if err := json.Unmarshal(filter["fromBlock"], &from); err != nil {
			t.Fatalf("source %d fromBlock: %v", i, err)
		}
		if err := json.Unmarshal(filter["toBlock"], &to); err != nil {
			t.Fatalf("source %d toBlock: %v", i, err)
		}
		if from != "0x0" || to != "0x10" {
			t.Fatalf("source %d range=%q..%q, want pinned 0x0..0x10", i, from, to)
		}
		if _, ok := filter["blockHash"]; ok {
			t.Fatalf("source %d range request unexpectedly carried blockHash: %s", i, req.Params[0])
		}
		var addresses []string
		if err := json.Unmarshal(filter["address"], &addresses); err != nil || len(addresses) != 1 || addresses[0] != address.Hex() {
			t.Fatalf("source %d address=%s err=%v", i, filter["address"], err)
		}
		var topics [][]string
		if err := json.Unmarshal(filter["topics"], &topics); err != nil || len(topics) != 1 || len(topics[0]) != 1 || topics[0][0] != topic.Hex() {
			t.Fatalf("source %d topics=%s err=%v", i, filter["topics"], err)
		}
	}
}

func TestTypedFilterLogsNullAndEmptyRemainDistinctAcrossTransports(t *testing.T) {
	for _, tc := range []struct {
		name       string
		response   string
		wantErr    error
		wantNonNil bool
	}{
		{name: "empty array", response: "[]", wantNonNil: true},
		{name: "null", response: "null", wantErr: ethguard.ErrMalformedResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawBlock := fixtureBlock(t, 16)
			var records [2]atomic.Value
			peers := []*sourceWirePeer{
				newSourceWirePeer(t, false, filterLogsTransportAnswer(rawBlock, []byte(tc.response), &records[0])),
				newSourceWirePeer(t, true, filterLogsTransportAnswer(rawBlock, []byte(tc.response), &records[1])),
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

			logs, _, callErr := client.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: big.NewInt(0)})
			if tc.wantErr != nil {
				if !errors.Is(callErr, tc.wantErr) || logs != nil {
					t.Fatalf("logs=%v err=%v, want nil and %v", logs, callErr, tc.wantErr)
				}
			} else if callErr != nil || len(logs) != 0 || (logs == nil) != !tc.wantNonNil {
				t.Fatalf("logs=%v err=%v, want non-nil empty array", logs, callErr)
			}
			for i, slot := range records {
				if loaded := slot.Load(); loaded == nil || loaded.(sourceWireRequest).Method != methods.MethodGetLogs {
					t.Fatalf("source %d did not record eth_getLogs: %v", i, loaded)
				}
			}
		})
	}
}

func TestTypedFilterLogsBlockHashAndRangeBinding(t *testing.T) {
	t.Run("block hash pin", func(t *testing.T) {
		rawBlock := fixtureBlock(t, 16)
		blockHash := blockHashFromFixture(t, rawBlock)
		rawLogs := transportLogs(t, blockHash, "0x10")
		requested := common.HexToHash(blockHash)
		var record atomic.Value
		peer := newSourceWirePeer(t, true, filterLogsTransportAnswer(rawBlock, rawLogs, &record))
		client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{{ID: "wss", Endpoint: peer.endpoint, Transport: config.TransportWSS}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		logs, _, err := client.FilterLogs(ctx, ethereum.FilterQuery{BlockHash: &requested})
		if err != nil || len(logs) != 1 || logs[0].BlockHash != requested {
			t.Fatalf("blockHash logs=%+v err=%v", logs, err)
		}
		if peer.headerRequests.Load() != 0 {
			t.Fatalf("blockHash query unexpectedly established a moving reference: %d header requests", peer.headerRequests.Load())
		}
		loaded := record.Load()
		if loaded == nil {
			t.Fatal("blockHash query did not record eth_getLogs")
		}
		req := loaded.(sourceWireRequest)
		var filter map[string]json.RawMessage
		if err := json.Unmarshal(req.Params[0], &filter); err != nil {
			t.Fatal(err)
		}
		var gotHash string
		if err := json.Unmarshal(filter["blockHash"], &gotHash); err != nil || gotHash != blockHash {
			t.Fatalf("blockHash filter=%s err=%v, want %q", filter["blockHash"], err, blockHash)
		}
		if _, ok := filter["fromBlock"]; ok {
			t.Fatalf("blockHash query unexpectedly carried fromBlock: %s", req.Params[0])
		}
		if _, ok := filter["toBlock"]; ok {
			t.Fatalf("blockHash query unexpectedly carried toBlock: %s", req.Params[0])
		}
	})

	t.Run("block hash response binding", func(t *testing.T) {
		rawBlock := fixtureBlock(t, 16)
		actualHash := blockHashFromFixture(t, rawBlock)
		rawLogs := transportLogs(t, actualHash, "0x10")
		var record atomic.Value
		peer := newSourceWirePeer(t, true, filterLogsTransportAnswer(rawBlock, rawLogs, &record))
		client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{{ID: "wss", Endpoint: peer.endpoint, Transport: config.TransportWSS}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		foreignHash := common.HexToHash("0x" + strings.Repeat("11", 32))
		_, _, err = client.FilterLogs(ctx, ethereum.FilterQuery{BlockHash: &foreignHash})
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("foreign blockHash response err=%v, want ErrMalformedResponse", err)
		}
		if loaded := record.Load(); loaded == nil || loaded.(sourceWireRequest).Method != methods.MethodGetLogs {
			t.Fatalf("foreign blockHash query was not recorded: %v", loaded)
		}
	})

	t.Run("range response binding", func(t *testing.T) {
		rawBlock := fixtureBlock(t, 16)
		blockHash := blockHashFromFixture(t, rawBlock)
		rawLogs := transportLogs(t, blockHash, "0x10")
		var records [2]atomic.Value
		peers := []*sourceWirePeer{
			newSourceWirePeer(t, false, filterLogsTransportAnswer(rawBlock, rawLogs, &records[0])),
			newSourceWirePeer(t, true, filterLogsTransportAnswer(rawBlock, rawLogs, &records[1])),
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
		_, _, err = client.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: big.NewInt(0), ToBlock: big.NewInt(15)})
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("out-of-range log err=%v, want ErrMalformedResponse", err)
		}
		for i, slot := range records {
			if loaded := slot.Load(); loaded == nil || loaded.(sourceWireRequest).Method != methods.MethodGetLogs {
				t.Fatalf("source %d did not record range request: %v", i, loaded)
			}
		}
	})

	t.Run("mixed selectors are rejected locally", func(t *testing.T) {
		peers := []*sourceWirePeer{
			newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":[]` }),
			newSourceWirePeer(t, true, func(sourceWireRequest) string { return `"result":[]` }),
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
		mixedHash := common.HexToHash("0x" + strings.Repeat("11", 32))
		_, _, err = client.FilterLogs(ctx, ethereum.FilterQuery{BlockHash: &mixedHash, FromBlock: big.NewInt(1)})
		if err == nil {
			t.Fatal("mixed blockHash/range filter unexpectedly succeeded")
		}
		for i, peer := range peers {
			if peer.requests.Load() != 0 {
				t.Fatalf("source %d received %d requests after mixed selector rejection", i, peer.requests.Load())
			}
		}
	})
}

func TestTypedFilterLogsRejectsInvalidRangesWithoutUpstreamRequests(t *testing.T) {
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":[]` }),
		newSourceWirePeer(t, true, func(sourceWireRequest) string { return `"result":[]` }),
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

	filters := []struct {
		name string
		q    ethereum.FilterQuery
	}{
		{name: "reversed", q: ethereum.FilterQuery{FromBlock: big.NewInt(3), ToBlock: big.NewInt(2)}},
		{name: "pending from", q: ethereum.FilterQuery{FromBlock: big.NewInt(-1)}},
		{name: "pending to", q: ethereum.FilterQuery{ToBlock: big.NewInt(-1)}},
		{name: "unsupported negative", q: ethereum.FilterQuery{FromBlock: big.NewInt(-6)}},
	}
	for _, filter := range filters {
		_, _, err := client.FilterLogs(ctx, filter.q)
		if !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("%s: got %v, want ErrInvalidRequest", filter.name, err)
		}
	}
	for i, peer := range peers {
		if got := peer.requests.Load(); got != 0 {
			t.Fatalf("source %d received %d requests after local range rejection", i, got)
		}
	}
}

func filterLogsTransportAnswer(rawBlock, rawLogs []byte, record *atomic.Value) func(sourceWireRequest) string {
	return func(req sourceWireRequest) string {
		if req.Method == methods.MethodGetBlockByNumber {
			return `"result":` + string(rawBlock)
		}
		if req.Method == methods.MethodGetLogs {
			record.Store(req)
			return `"result":` + string(rawLogs)
		}
		return `"result":"0x1"`
	}
}

func blockHashFromFixture(t *testing.T, raw []byte) string {
	t.Helper()
	var block struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &block); err != nil || block.Hash == "" {
		t.Fatalf("fixture block=%s err=%v", raw, err)
	}
	return block.Hash
}

func transportLogs(t *testing.T, blockHash, blockNumber string) []byte {
	t.Helper()
	logs := []map[string]any{{
		"address":          "0x0000000000000000000000000000000000000002",
		"blockNumber":      blockNumber,
		"blockHash":        blockHash,
		"transactionHash":  "0x00000000000000000000000000000000000000000000000000000000000000cd",
		"transactionIndex": "0x0",
		"logIndex":         "0x0",
		"topics":           []string{},
		"data":             "0x",
		"removed":          false,
	}}
	raw, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

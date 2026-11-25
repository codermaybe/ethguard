package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"

	"github.com/codermaybe/ethguard/ethapi"
)

type logsBlockFixture struct {
	head     *types.Header
	txs      types.Transactions
	block    json.RawMessage
	receipts []map[string]any
}

func logsJSON(t testing.TB, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func makeLogsBlock(t testing.TB, parent *types.Header, number uint64, salt byte, counts ...int) logsBlockFixture {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	address := common.HexToAddress("0x1111111111111111111111111111111111111111")
	txs := make(types.Transactions, len(counts))
	receipts := make(types.Receipts, len(counts))
	for i, count := range counts {
		txs[i], err = types.SignNewTx(key, types.LatestSignerForChainID(big.NewInt(1)), &types.LegacyTx{Nonce: uint64(i), To: &address, Gas: 21000, GasPrice: big.NewInt(2), Value: big.NewInt(0)})
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = &types.Receipt{Status: 1, CumulativeGasUsed: uint64(i+1) * 21000, Logs: make([]*types.Log, count)}
		for j := 0; j < count; j++ {
			receipts[i].Logs[j] = &types.Log{Address: address, Topics: []common.Hash{common.BigToHash(big.NewInt(int64(i + 1)))}, Data: []byte{byte(j), salt}}
		}
		receipts[i].Bloom = types.CreateBloom(receipts[i])
	}
	h := &types.Header{UncleHash: types.EmptyUncleHash, Number: new(big.Int).SetUint64(number), Difficulty: big.NewInt(1), Time: 123 + number, GasLimit: 30000000, GasUsed: uint64(len(txs)) * 21000, Extra: []byte{salt}, TxHash: types.DeriveSha(txs, trie.NewStackTrie(nil)), ReceiptHash: types.DeriveSha(receipts, trie.NewStackTrie(nil)), Bloom: types.MergeBloom(receipts)}
	if parent != nil {
		h.ParentHash = parent.Hash()
	}
	rows := make([]map[string]any, len(txs))
	transactions := make([]map[string]any, len(txs))
	index := 0
	for i, r := range receipts {
		entries := make([]map[string]any, len(r.Logs))
		for j, entry := range r.Logs {
			entries[j] = map[string]any{"address": address.Hex(), "topics": entry.Topics, "data": hexutil.Encode(entry.Data), "blockHash": h.Hash().Hex(), "blockNumber": hexutil.EncodeUint64(number), "transactionHash": txs[i].Hash().Hex(), "transactionIndex": hexutil.EncodeUint64(uint64(i)), "logIndex": hexutil.EncodeUint64(uint64(index)), "removed": false}
			index++
		}
		rows[i] = map[string]any{"type": "0x0", "status": "0x1", "cumulativeGasUsed": hexutil.EncodeUint64(r.CumulativeGasUsed), "gasUsed": "0x5208", "effectiveGasPrice": "0x2", "logsBloom": hexutil.Encode(r.Bloom.Bytes()), "transactionHash": txs[i].Hash().Hex(), "transactionIndex": hexutil.EncodeUint64(uint64(i)), "blockHash": h.Hash().Hex(), "blockNumber": hexutil.EncodeUint64(number), "from": crypto.PubkeyToAddress(key.PublicKey).Hex(), "to": address.Hex(), "contractAddress": nil, "logs": entries}
		if err := json.Unmarshal(logsJSON(t, txs[i]), &transactions[i]); err != nil {
			t.Fatal(err)
		}
		transactions[i]["from"] = crypto.PubkeyToAddress(key.PublicKey).Hex()
		transactions[i]["blockHash"] = h.Hash().Hex()
		transactions[i]["blockNumber"] = hexutil.EncodeUint64(number)
		transactions[i]["transactionIndex"] = hexutil.EncodeUint64(uint64(i))
		transactions[i]["blockTimestamp"] = hexutil.EncodeUint64(h.Time)
	}
	var block map[string]any
	if err := json.Unmarshal(testHeader(h), &block); err != nil {
		t.Fatal(err)
	}
	block["transactions"], block["uncles"], block["size"] = transactions, []string{}, "0x321"
	return logsBlockFixture{head: h, txs: txs, block: logsJSON(t, block), receipts: rows}
}

func TestLogsContentProvesAndConstructsEveryLocation(t *testing.T) {
	for _, counts := range [][]int{nil, {0}, {2, 0, 1}} {
		t.Run(fmt.Sprint(counts), func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, counts...)
			txs, err := verifyBlockTransactions(context.Background(), f.head, f.block, nil)
			if err != nil {
				t.Fatal(err)
			}
			logs, err := proveBlockLogs(f.head, txs, logsJSON(t, f.receipts))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			for _, count := range counts {
				want += count
			}
			if logs == nil || len(logs) != want {
				t.Fatalf("logs = %v, want %d", logs, want)
			}
			for i, entry := range logs {
				if *entry.BlockHash != ethapi.Hash(f.head.Hash().Hex()) || *entry.BlockNumber != "0x64" || *entry.BlockTimestamp != "0xdf" || *entry.Removed || *entry.LogIndex != ethapi.Quantity(fmt.Sprintf("0x%x", i)) {
					t.Fatalf("incorrect location: %+v", entry)
				}
				if _, err := ethapi.NewLog(logsJSON(t, entry)); err != nil {
					t.Fatal(err)
				}
			}
			if len(logs) > 1 {
				*logs[0].BlockHash = "mutated"
				if *logs[1].BlockHash == "mutated" {
					t.Fatal("logs share mutable location pointer")
				}
			}
		})
	}
}

func TestLogsContentRejectsUnprovedLocations(t *testing.T) {
	cases := map[string]any{"blockHash": common.Hash{}.Hex(), "blockNumber": "0x65", "transactionHash": common.Hash{}.Hex(), "transactionIndex": "0x1", "logIndex": "0x1", "removed": true, "blockTimestamp": "0x1"}
	for field, value := range cases {
		t.Run(field, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			f.receipts[0]["logs"].([]map[string]any)[0][field] = value
			if logs, err := proveBlockLogs(f.head, f.txs, logsJSON(t, f.receipts)); err == nil || logs != nil {
				t.Fatalf("accepted %s: %v", field, err)
			}
		})
	}
}

func TestLogsContentRequiresCompleteCommittedReceipts(t *testing.T) {
	for _, change := range []string{"missing", "duplicate", "reordered", "changed-content", "changed-tx"} {
		t.Run(change, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1, 1)
			switch change {
			case "missing":
				f.receipts = f.receipts[:1]
			case "duplicate":
				f.receipts[1] = f.receipts[0]
			case "reordered":
				f.receipts[0], f.receipts[1] = f.receipts[1], f.receipts[0]
			case "changed-content":
				f.receipts[0]["logs"].([]map[string]any)[0]["data"] = "0xffff"
			case "changed-tx":
				f.txs[0], f.txs[1] = f.txs[1], f.txs[0]
			}
			if logs, err := proveBlockLogs(f.head, f.txs, logsJSON(t, f.receipts)); err == nil || logs != nil {
				t.Fatalf("accepted %s", change)
			}
		})
	}
	f := makeLogsBlock(t, nil, 100, 1, 1)
	f.receipts[0]["from"], f.receipts[0]["effectiveGasPrice"], f.receipts[0]["gasUsed"] = common.Address{}.Hex(), "0xffff", "0x1"
	if _, err := proveBlockLogs(f.head, f.txs, logsJSON(t, f.receipts)); err != nil {
		t.Fatalf("uncommitted receipt metadata affected log proof: %v", err)
	}
}

func TestLogsBlockReusesFullBlockAdmission(t *testing.T) {
	for _, change := range []string{"hash", "transactions", "sender", "txhash", "index"} {
		t.Run(change, func(t *testing.T) {
			f := makeLogsBlock(t, nil, 100, 1, 1)
			var block map[string]any
			_ = json.Unmarshal(f.block, &block)
			tx := block["transactions"].([]any)[0].(map[string]any)
			switch change {
			case "hash":
				block["hash"] = common.Hash{}.Hex()
			case "transactions":
				block["transactions"] = []any{}
			case "sender":
				delete(tx, "from")
			case "txhash":
				tx["hash"] = common.Hash{}.Hex()
			case "index":
				tx["transactionIndex"] = "0x1"
			}
			if txs, err := verifyBlockTransactions(context.Background(), f.head, logsJSON(t, block), nil); err == nil || txs != nil {
				t.Fatalf("accepted %s", change)
			}
		})
	}
}

func logsQuery(t *testing.T, raw string) ethapi.FilterQuery {
	t.Helper()
	q, err := ethapi.NewFilterQuery(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestLogsFilterOfficialMatchingAndRange(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 1)
	logs, err := proveBlockLogs(f.head, f.txs, logsJSON(t, f.receipts))
	if err != nil {
		t.Fatal(err)
	}
	address, topic := string(*logs[0].Address), string((*logs[0].Topics)[0])
	cases := []struct {
		raw  string
		want bool
	}{
		{`{}`, true}, {`{"address":null,"topics":null}`, true}, {`{"address":[],"topics":[]}`, true},
		{fmt.Sprintf(`{"address":%q,"topics":[%q]}`, address, topic), true},
		{fmt.Sprintf(`{"address":[%q,%q],"topics":[[%q,%q]]}`, common.Address{}.Hex(), address, common.Hash{}.Hex(), topic), true},
		{`{"topics":[null]}`, true}, {`{"topics":[[]]}`, true}, {`{"topics":[null,null]}`, false},
		{fmt.Sprintf(`{"address":%q}`, common.Address{}.Hex()), false},
		{fmt.Sprintf(`{"topics":[%q]}`, common.Hash{}.Hex()), false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			filter, err := compileLogsFilter(logsQuery(t, tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if filter.matches(logs[0]) != tc.want {
				t.Fatalf("match want %t", tc.want)
			}
			if tc.want && !filter.mayMatch(f.head.Bloom) {
				t.Fatal("bloom excluded actual match")
			}
		})
	}
	for _, raw := range []string{`{"fromBlock":"safe"}`, `{"toBlock":"finalized"}`, `{"extension":1}`, `{"fromBlock":"0x10","toBlock":"0xf"}`, `{"fromBlock":"0x10000000000000000"}`, `{"toBlock":"0x10000000000000000"}`} {
		if _, err := compileLogsFilter(logsQuery(t, raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	zero, lower, upper, maximum := uint64(0), uint64(100), uint64(101), uint64(math.MaxUint64)
	for _, tc := range []struct {
		raw        string
		from, to   *uint64
		boundaries map[uint64]bool
	}{
		{`{}`, &zero, nil, map[uint64]bool{0: true, maximum: true}},
		{`{"fromBlock":"latest","toBlock":"latest"}`, nil, nil, map[uint64]bool{0: true, maximum: true}},
		{`{"fromBlock":"earliest","toBlock":"earliest"}`, &zero, &zero, map[uint64]bool{0: true, 1: false}},
		{`{"fromBlock":"0x64","toBlock":"0x65"}`, &lower, &upper, map[uint64]bool{99: false, 100: true, 101: true, 102: false}},
		{`{"fromBlock":"0xffffffffffffffff","toBlock":"0xffffffffffffffff"}`, &maximum, &maximum, map[uint64]bool{maximum - 1: false, maximum: true}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			filter, err := compileLogsFilter(logsQuery(t, tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(filter.from, tc.from) || !reflect.DeepEqual(filter.to, tc.to) {
				t.Fatalf("bounds = %v/%v, want %v/%v", filter.from, filter.to, tc.from, tc.to)
			}
			for height, want := range tc.boundaries {
				if filter.includes(AnchorNode{Number: height}) != want {
					t.Fatalf("range at %d: want included=%t", height, want)
				}
			}
		})
	}
	filter, err := compileLogsFilter(logsQuery(t, fmt.Sprintf(`{"blockHash":%q}`, f.head.Hash().Hex())))
	if err != nil || !filter.includes(AnchorNode{Hash: f.head.Hash()}) || filter.includes(AnchorNode{}) {
		t.Fatalf("hash filter: %v", err)
	}
}

func TestLogsFilterOwnsQueryAndPreservesGlobalIndicesAfterMatching(t *testing.T) {
	f := makeLogsBlock(t, nil, 100, 1, 2, 1)
	all, err := proveBlockLogs(f.head, f.txs, logsJSON(t, f.receipts))
	if err != nil {
		t.Fatal(err)
	}
	query := logsQuery(t, fmt.Sprintf(`{"fromBlock":"0x64","toBlock":"0x65","address":[%q],"topics":[[%q]]}`, *all[0].Address, (*all[2].Topics)[0]))
	(*query.FilterByBlockRange.Address.Addresses)[0] = ethapi.Address(strings.ToUpper(string(*all[0].Address)))
	(*(**query.FilterByBlockRange.Topics)[0].MultipleTopicMatch)[0] = ethapi.Hash(strings.ToUpper(string((*all[2].Topics)[0])))
	filter, err := compileLogsFilter(query)
	if err != nil {
		t.Fatal(err)
	}
	(*query.FilterByBlockRange.Address.Addresses)[0] = ethapi.Address(common.Address{}.Hex())
	(*(**query.FilterByBlockRange.Topics)[0].MultipleTopicMatch)[0] = ethapi.Hash(common.Hash{}.Hex())
	*query.FilterByBlockRange.FromBlock.BlockNumber = "0x0"
	*query.FilterByBlockRange.ToBlock.BlockNumber = "0x0"
	for n, want := range map[uint64]bool{99: false, 100: true, 101: true, 102: false} {
		if filter.includes(AnchorNode{Number: n}) != want {
			t.Fatalf("range changed after caller mutation at %d", n)
		}
	}
	var matched []ethapi.Log
	for _, entry := range all {
		if filter.matches(entry) {
			matched = append(matched, entry)
		}
	}
	if len(matched) != 1 || *matched[0].LogIndex != "0x2" || *matched[0].TransactionIndex != "0x1" {
		t.Fatalf("filter altered global location or was not frozen: %+v", matched)
	}
	hashQuery := ethapi.FilterQuery{FilterByBlockHash: &ethapi.FilterQueryHash{BlockHash: ethapi.Hash(strings.ToUpper(f.head.Hash().Hex()))}}
	hashFilter, err := compileLogsFilter(hashQuery)
	if err != nil {
		t.Fatal(err)
	}
	hashQuery.FilterByBlockHash.BlockHash = ethapi.Hash(common.Hash{}.Hex())
	if !hashFilter.includes(AnchorNode{Hash: f.head.Hash()}) || hashFilter.includes(AnchorNode{}) {
		t.Fatal("hash filter was not canonicalized and frozen")
	}
}

func BenchmarkCompileLogsFilter(b *testing.B) {
	for _, tc := range []struct{ name, raw string }{
		{"empty", `{}`},
		{"nested", `{"fromBlock":"0x64","toBlock":"latest","address":["0xcccccccccccccccccccccccccccccccccccccccc","0xdddddddddddddddddddddddddddddddddddddddd"],"topics":[null,["0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]]}`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			query, err := ethapi.NewFilterQuery(json.RawMessage(tc.raw))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := compileLogsFilter(query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

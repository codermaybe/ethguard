package block

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"

	"github.com/codermaybe/ethguard/rpc"
)

func marshalBlockBenchmarkValue(tb testing.TB, value any) json.RawMessage {
	tb.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

// completeBenchmarkBlock builds distinct, signed transfers and a header whose
// transaction trie commits to them. Each transaction carries the containing
// hash, height, timestamp and its actual index. This synthetic source-local
// admission fixture is not a provider sample or proof of chain execution.
func completeBenchmarkBlock(tb testing.TB, count int) (json.RawMessage, rpc.Request) {
	tb.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		tb.Fatal(err)
	}
	to := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	txs := make(types.Transactions, count)
	for i := range txs {
		txs[i], err = types.SignTx(types.NewTransaction(uint64(i), to, big.NewInt(1), 21000, big.NewInt(1), nil), types.NewEIP155Signer(big.NewInt(1)), key)
		if err != nil {
			tb.Fatal(err)
		}
	}
	head := &types.Header{
		UncleHash: types.EmptyUncleHash, TxHash: types.DeriveSha(txs, trie.NewStackTrie(nil)),
		Difficulty: big.NewInt(1), Number: big.NewInt(8), Time: 12,
		GasLimit: 30000000, GasUsed: uint64(count) * 21000,
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(marshalBlockBenchmarkValue(tb, head), &fields); err != nil {
		tb.Fatal(err)
	}
	rows := make([]json.RawMessage, count)
	for i, tx := range txs {
		var transaction map[string]json.RawMessage
		if err := json.Unmarshal(marshalBlockBenchmarkValue(tb, tx), &transaction); err != nil {
			tb.Fatal(err)
		}
		transaction["from"] = marshalBlockBenchmarkValue(tb, crypto.PubkeyToAddress(key.PublicKey).Hex())
		transaction["blockHash"] = marshalBlockBenchmarkValue(tb, head.Hash().Hex())
		transaction["blockNumber"] = json.RawMessage(`"0x8"`)
		transaction["blockTimestamp"] = json.RawMessage(`"0xc"`)
		transaction["transactionIndex"] = marshalBlockBenchmarkValue(tb, fmt.Sprintf("0x%x", i))
		rows[i] = marshalBlockBenchmarkValue(tb, transaction)
	}
	fields["transactions"] = marshalBlockBenchmarkValue(tb, rows)
	fields["uncles"] = json.RawMessage(`[]`)
	block := types.NewBlockWithHeader(head).WithBody(types.Body{Transactions: txs})
	fields["size"] = marshalBlockBenchmarkValue(tb, fmt.Sprintf("0x%x", block.Size()))
	return marshalBlockBenchmarkValue(tb, fields), rpc.Request{Method: "eth_getBlockByHash", Params: []any{head.Hash().Hex(), true}}
}

// BenchmarkBlockObject uses the same fully admitted fixture for the shape
// encoder and the complete source-local admission path. Signature creation,
// trie construction and the admission/delivery warm-up stay outside timing.
func BenchmarkBlockObject(b *testing.B) {
	for _, count := range []int{1, 100} {
		b.Run(fmt.Sprintf("transactions-%d", count), func(b *testing.B) {
			raw, req := completeBenchmarkBlock(b, count)
			full, err := AdmitBlock(b.Context(), req, raw, true, nil)
			if err != nil {
				b.Fatalf("fixture admission: %v", err)
			}
			txs, err := full.Transactions()
			if err != nil || len(txs) != count {
				b.Fatalf("fixture delivery count = %d, error = %v", len(txs), err)
			}
			b.Run("Encode", func(b *testing.B) {
				b.SetBytes(int64(len(raw)))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := Encode(raw); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("AdmitBlock", func(b *testing.B) {
				b.SetBytes(int64(len(raw)))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := AdmitBlock(b.Context(), req, raw, true, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func TestBlockBenchmarkFixturePassesFullAdmission(t *testing.T) {
	for _, count := range []int{1, 100} {
		t.Run(fmt.Sprintf("transactions-%d", count), func(t *testing.T) {
			raw, req := completeBenchmarkBlock(t, count)
			full, err := AdmitBlock(t.Context(), req, raw, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			txs, err := full.Transactions()
			if err != nil || len(txs) != count {
				t.Fatalf("transactions = %d, error = %v", len(txs), err)
			}
			for i, tx := range txs {
				if tx.Nonce() != uint64(i) {
					t.Fatalf("transaction %d nonce = %d", i, tx.Nonce())
				}
			}
		})
	}
}

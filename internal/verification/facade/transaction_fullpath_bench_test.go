package ethguard_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpctest"
)

// The full production-path benchmark for the transaction pilot: the typed
// facade entry through the runtime, aggregator, pool fan-out, admission, and
// typed decode, against scripted same-process sources. This complements the
// local hook measurements in rpc/methods: it measures what one invocation
// costs end to end, including the phases the local numbers exclude. The
// sources are synthetic in-process nodes, so absolute times carry no provider
// network meaning; the value is the same-harness before/after comparison and
// the allocation profile.

// benchFleet is the benchmark-shaped fleet helper: the same construction the
// test fleet uses, without t.Cleanup-dependent teardown semantics.
func benchFleet(b *testing.B, count int) (*ethguard.Client, []*rpctest.Node) {
	b.Helper()
	nodes := make([]*rpctest.Node, 0, count)
	cfg := config.Config{ChainID: 1}
	for i := range count {
		node := rpctest.NewNode(1)
		nodes = append(nodes, node)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%c", 'a'+i),
			Endpoint: node.URL(),
		})
	}
	client, err := ethguard.New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		client.Close()
		for _, node := range nodes {
			node.Close()
		}
	})
	return client, nodes
}

// transactionBenchFixture assembles one official mined type-2 transaction
// object with a data input of dataSize bytes and an access list of entries
// addresses, signed and shaped exactly like the compatibility fixture.
func transactionBenchFixture(b *testing.B, dataSize, entries int) (json.RawMessage, common.Hash) {
	b.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		b.Fatal(err)
	}
	to := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	data := bytes.Repeat([]byte{0xab}, dataSize)
	signed, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(1), Nonce: 7, Gas: 30000, To: &to,
		Value: big.NewInt(1), GasFeeCap: big.NewInt(5), GasTipCap: big.NewInt(2),
		Data: data,
		AccessList: func() types.AccessList {
			list := make(types.AccessList, entries)
			for i := range entries {
				list[i] = types.AccessTuple{
					Address:     common.HexToAddress(fmt.Sprintf("0x%064x", i+1)),
					StorageKeys: []common.Hash{common.HexToHash(fmt.Sprintf("0x%064x", i+1))},
				}
			}
			return list
		}(),
	}), types.NewLondonSigner(big.NewInt(1)), key)
	if err != nil {
		b.Fatal(err)
	}
	v, r, s := signed.RawSignatureValues()
	fields := map[string]any{
		"type": "0x2", "chainId": "0x1", "nonce": "0x7", "gas": "0x7530",
		"to": strings.ToLower(to.Hex()), "value": "0x1", "input": "0x" + hex.EncodeToString(data),
		"maxFeePerGas": "0x5", "maxPriorityFeePerGas": "0x2", "gasPrice": "0x3",
		"accessList": func() []any {
			list := make([]any, entries)
			for i := range entries {
				list[i] = map[string]any{
					"address":     strings.ToLower(common.HexToAddress(fmt.Sprintf("0x%064x", i+1)).Hex()),
					"storageKeys": []string{common.HexToHash(fmt.Sprintf("0x%064x", i+1)).Hex()},
				}
			}
			return list
		}(),
		"yParity": hexutil.EncodeBig(v), "v": hexutil.EncodeBig(v),
		"r": hexutil.EncodeBig(r), "s": hexutil.EncodeBig(s),
		"hash": signed.Hash().Hex(), "from": strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex()),
		"blockHash": transactionCompatibilityBlock, "blockNumber": "0x10",
		"blockTimestamp": "0x5", "transactionIndex": "0x0",
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		b.Fatal(err)
	}
	return raw, signed.Hash()
}

// transactionBenchInvalid is the same-shaped response missing one signature
// member: it passes JSON safety and fails admission, which is the partial
// and all-invalid cost shapes.
func transactionBenchInvalid(b *testing.B, raw json.RawMessage) json.RawMessage {
	b.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		b.Fatal(err)
	}
	delete(fields, "r")
	out, err := json.Marshal(fields)
	if err != nil {
		b.Fatal(err)
	}
	return out
}

func benchmarkTransactionProductionPath(b *testing.B, method string) {
	shapes := []string{"all-valid", "partial-invalid", "all-invalid"}
	for _, count := range []int{1, 3, 5} {
		for _, shape := range shapes {
			if count == 1 && shape == "partial-invalid" {
				continue // one source cannot be partially invalid
			}
			name := fmt.Sprintf("%s/sources=%d/%s", method, count, shape)
			b.Run(name, func(b *testing.B) {
				raw, hash := transactionBenchFixture(b, 128, 2)
				bad := transactionBenchInvalid(b, raw)
				client, nodes := benchFleet(b, count)
				for i, node := range nodes {
					switch {
					case shape == "all-valid":
						node.ReturnRaw(method, raw)
					case shape == "partial-invalid":
						if i < count/3 {
							node.ReturnRaw(method, bad)
						} else {
							node.ReturnRaw(method, raw)
						}
					default:
						node.ReturnRaw(method, bad)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				b.ReportAllocs()
				b.ResetTimer()
				// Only the all-invalid shape must fail: with two or more valid
				// sources the dynamic quorum still corroborates, which is the
				// partial cost shape.
				for i := 0; i < b.N; i++ {
					if method == "eth_getTransactionByHash" {
						_, _, _, err := client.TransactionByHash(ctx, hash)
						if shape == "all-invalid" && err == nil {
							b.Fatalf("all-invalid shape succeeded")
						}
						if shape != "all-invalid" && err != nil {
							b.Fatal(err)
						}
					} else {
						_, _, err := client.TransactionInBlock(ctx, common.HexToHash(transactionCompatibilityBlock), 0)
						if shape == "all-invalid" && err == nil {
							b.Fatalf("all-invalid shape succeeded")
						}
						if shape != "all-invalid" && err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

func BenchmarkTransactionByHashProductionPath(b *testing.B) {
	benchmarkTransactionProductionPath(b, "eth_getTransactionByHash")
}

func BenchmarkTransactionInBlockProductionPath(b *testing.B) {
	benchmarkTransactionProductionPath(b, "eth_getTransactionByBlockHashAndIndex")
}

// BenchmarkTransactionByHashLargeInput measures the same path with a large
// data input and a non-empty access list — the response-size sensitivity the
// small fixture cannot show.
func BenchmarkTransactionByHashLargeInput(b *testing.B) {
	for _, size := range []struct {
		name            string
		dataSize        int
		accessListCount int
	}{
		{"input=8KB/accessList=8", 8192, 8},
		{"input=64KB/accessList=32", 65536, 32},
	} {
		b.Run(size.name, func(b *testing.B) {
			raw, hash := transactionBenchFixture(b, size.dataSize, size.accessListCount)
			client, nodes := benchFleet(b, 3)
			for _, node := range nodes {
				node.ReturnRaw("eth_getTransactionByHash", raw)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _, err := client.TransactionByHash(ctx, hash)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

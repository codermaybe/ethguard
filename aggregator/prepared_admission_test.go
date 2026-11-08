package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// Exercise the built-in plans through the wire, admission and resolution. The
// full-body flag must mean the frozen JSON value, not its original Go type or
// the value a custom marshaler would return on a second call.
func TestPreparedBlockAdmissionUsesFrozenSemanticParams(t *testing.T) {
	for _, method := range []string{methods.MethodGetBlockByNumber, methods.MethodGetBlockByHash} {
		t.Run(method, func(t *testing.T) {
			for _, flag := range []struct {
				name string
				make func() any
			}{
				{"bool", func() any { return true }},
				{"pointer-bool", func() any {
					value := true
					return &value
				}},
				{"named-bool", func() any { return frozenFullBlockFlag(true) }},
				{"changing-marshaler", func() any { return &firstTrueMarshaler{} }},
			} {
				t.Run(flag.name, func(t *testing.T) {
					for _, response := range []string{"valid", "header-only", "hash-only-body", "wrong-root", "wrong-wire-hash", "wrong-request-hash"} {
						if response == "wrong-request-hash" && method != methods.MethodGetBlockByHash {
							continue
						}
						t.Run(response, func(t *testing.T) {
							raw, hash := frozenAdmissionBlock(t, response)
							ref := "0x1"
							if method == methods.MethodGetBlockByHash {
								ref = hash.Hex()
								if response == "wrong-request-hash" {
									ref = common.HexToHash("0x99").Hex()
								}
							}
							full := flag.make()
							decision, err := frozenAdmissionAggregator(t, method, raw).Aggregate(context.Background(),
								rpc.Request{Method: method, Params: []any{ref, full}})
							if custom, ok := full.(*firstTrueMarshaler); ok && custom.calls != 1 {
								t.Fatalf("marshaler ran %d times, want exactly one", custom.calls)
							}
							if response == "valid" {
								if err != nil || decision.Status != rpc.Corroborated || len(decision.Value) == 0 {
									t.Fatalf("valid full block rejected: status %v, value %s, err %v", decision.Status, decision.Value, err)
								}
							} else if !errors.Is(err, rpc.ErrMalformedResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
								t.Fatalf("unsafe full block admitted: status %v, value %s, err %v", decision.Status, decision.Value, err)
							}
							if decision.Prepared == nil {
								t.Fatal("prepared request was not recorded")
							}
							var params []json.RawMessage
							if err := json.Unmarshal(decision.Prepared.Params, &params); err != nil {
								t.Fatal(err)
							}
							if len(params) != 2 || string(params[1]) != "true" {
								t.Fatalf("prepared params = %s, want frozen full-body flag true", decision.Prepared.Params)
							}
						})
					}
				})
			}
		})
	}
}

func TestPreparedBlockAdmissionAcceptsHeaderOnlyFalse(t *testing.T) {
	for _, method := range []string{methods.MethodGetBlockByNumber, methods.MethodGetBlockByHash} {
		t.Run(method, func(t *testing.T) {
			raw, hash := frozenAdmissionBlock(t, "header-only")
			ref := "0x1"
			if method == methods.MethodGetBlockByHash {
				ref = hash.Hex()
			}
			decision, err := frozenAdmissionAggregator(t, method, raw).Aggregate(context.Background(),
				rpc.Request{Method: method, Params: []any{ref, false}})
			if err != nil || decision.Status != rpc.Corroborated || len(decision.Value) == 0 {
				t.Fatalf("header-only block rejected: status %v, value %s, err %v", decision.Status, decision.Value, err)
			}
		})
	}
}

func TestPreparedAdmissionPreservesFeeHistoryNumericCount(t *testing.T) {
	const method = methods.MethodFeeHistory
	raw := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`)
	decision, err := frozenAdmissionAggregator(t, method, raw).Aggregate(context.Background(),
		rpc.Request{Method: method, Params: []any{uint64(1), "0x1", []float64{}}})
	if err != nil || decision.Status != rpc.Corroborated || len(decision.Value) == 0 {
		t.Fatalf("numeric fee-history count rejected: status %v, value %s, err %v", decision.Status, decision.Value, err)
	}
}

func TestPreparedAdmissionPreservesNumericTransactionIndex(t *testing.T) {
	const method = methods.MethodGetTransactionByBlockHashAndIndex
	key, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x2")
	tx, err := types.SignNewTx(key, types.LatestSignerForChainID(big.NewInt(1)), &types.LegacyTx{
		To: &to, Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	blockHash := common.HexToHash("0x3").Hex()
	body["from"] = crypto.PubkeyToAddress(key.PublicKey).Hex()
	body["blockHash"] = blockHash
	body["blockNumber"] = "0x1"
	for _, index := range []struct {
		name  string
		param any
		value uint64
	}{
		{"uint", uint(1), 1},
		{"uint64-above-float-precision", uint64(1<<53 + 1), 1<<53 + 1},
		{"json-number-max-uint64", json.Number("18446744073709551615"), ^uint64(0)},
	} {
		t.Run(index.name, func(t *testing.T) {
			for _, mismatch := range []bool{false, true} {
				name, returnedIndex := "matching", index.value
				if mismatch {
					name, returnedIndex = "mismatching", index.value^1
				}
				t.Run(name, func(t *testing.T) {
					body["transactionIndex"] = hexutil.EncodeUint64(returnedIndex)
					raw, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					decision, err := frozenAdmissionAggregator(t, method, raw).Aggregate(context.Background(),
						rpc.Request{Method: method, Params: []any{blockHash, index.param}})
					if mismatch {
						if !errors.Is(err, rpc.ErrMalformedResponse) || len(decision.Value) != 0 {
							t.Fatalf("wrong index admitted: value %s, err %v", decision.Value, err)
						}
					} else if err != nil || decision.Status != rpc.Corroborated || len(decision.Value) == 0 {
						t.Fatalf("numeric transaction index rejected: status %v, value %s, err %v", decision.Status, decision.Value, err)
					}
				})
			}
		})
	}
}

func frozenAdmissionAggregator(t *testing.T, method string, raw json.RawMessage) *Aggregator {
	t.Helper()
	nodes, p := probeNodes(t, "0x1", 2)
	for _, node := range nodes {
		node.ReturnRaw(method, raw)
	}
	return New(p, WithCorroboration(mustShared(t)), WithPlans(map[string]rpc.ReadPlan{method: planFor(t, method)}))
}

func frozenAdmissionBlock(t *testing.T, variant string) (json.RawMessage, common.Hash) {
	t.Helper()
	header := &types.Header{
		ParentHash:  common.HexToHash("0x1"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x2"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: types.EmptyReceiptsHash,
		Difficulty:  new(big.Int),
		Number:      big.NewInt(1),
		GasLimit:    1,
		Time:        1,
	}
	if variant == "wrong-root" {
		header.TxHash = common.HexToHash("0x3")
	}
	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["transactions"] = json.RawMessage(`[]`)
	body["uncles"] = json.RawMessage(`[]`)
	switch variant {
	case "header-only":
		body = map[string]json.RawMessage{"number": body["number"], "hash": body["hash"]}
	case "hash-only-body":
		body["transactions"] = json.RawMessage(`["` + common.HexToHash("0x4").Hex() + `"]`)
	case "wrong-wire-hash":
		body["hash"] = json.RawMessage(`"` + common.HexToHash("0x5").Hex() + `"`)
	}
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw, header.Hash()
}

type frozenFullBlockFlag bool

type firstTrueMarshaler struct {
	calls int
}

func (m *firstTrueMarshaler) MarshalJSON() ([]byte, error) {
	m.calls++
	if m.calls == 1 {
		return []byte("true"), nil
	}
	return []byte("false"), nil
}

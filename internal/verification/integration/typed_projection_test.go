package ethguard_test

// Production typed probes and summaries used by T09.2 round trips and the
// T09.1 matrix. There is no test-side typed decoder in these paths: every
// summary comes from the same ethguard Client result a caller
// receives.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/testkit/ethapitest"
	"github.com/codermaybe/ethguard/internal/typed"
)

// Typed summary helpers. Every summary is the production typed shape's own
// value; the corresponding TypedWant helper applies the production decoder to
// the scripted answer.

func t09SummaryBig(value *big.Int) string { return value.String() }

func t09SummaryQuantity(value uint64) string { return fmt.Sprint(value) }

func t09SummaryBytes(value []byte) string { return hexutil.Encode(value) }

func t09SummaryBlock(block *ethapi.Block) string {
	summary, err := t09BlockSummary(block)
	if err != nil {
		panic(err)
	}
	return summary
}

func t09SummaryHeader(header *ethapi.Header) string {
	raw, err := json.Marshal(header)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryReceipt(receipt *ethapi.Receipt) string {
	raw, err := json.Marshal(receipt)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryReceipts(receipts []ethapi.Receipt) string {
	raw, err := json.Marshal(receipts)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryTransaction(tx *ethapi.Transaction, pending bool) string {
	raw, err := json.Marshal(tx)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s|pending=%v", raw, pending)
}

func t09SummaryLogs(logs []ethapi.Log) string {
	raw, err := json.Marshal(logs)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryFee(fee *ethapi.FeeHistoryResult) string {
	raw, err := json.Marshal(fee)
	if err != nil {
		panic(err)
	}
	var want t09FeeWantWire
	if err := json.Unmarshal(raw, &want); err != nil {
		panic(err)
	}
	return t09FeeWireSummary(want)
}

func t09SummaryAccess(result *ethapi.AccessListResult) string {
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummarySync(progress *ethapi.SyncProgress) string {
	if progress == nil {
		return "false"
	}
	// T09 deliberately does not synthesize the progress-object state
	// (pipeline 5.1.1); the typed probe only sees the legal false answer.
	panic("t09: unexpected syncing progress object")
}

func t09TypedWantBig(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value hexutil.Big
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode expected quantity: %v", err)
	}
	return (*big.Int)(&value).String()
}

func t09TypedWantDecimal(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		t.Fatalf("decode expected decimal: %v", err)
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		t.Fatalf("decode expected decimal: %q", text)
	}
	return value.String()
}

func t09TypedWantQuantity(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value hexutil.Uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode expected quantity: %v", err)
	}
	return fmt.Sprint(uint64(value))
}

func t09TypedWantBytes(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value hexutil.Bytes
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode expected bytes: %v", err)
	}
	return hexutil.Encode(value)
}

func t09TypedWantBlock(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	block, err := ethguard.DecodeBlock(t09FoldNulls(t09SemanticJSON(t, raw)))
	if err != nil {
		t.Fatalf("decode expected block: %v", err)
	}
	return t09SummaryBlock(block)
}

func t09TypedWantHeader(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	header, err := ethguard.DecodeBlockHeaderOnly(t09SemanticJSON(t, raw))
	if err != nil {
		t.Fatalf("decode expected header: %v", err)
	}
	return t09SummaryHeader(header)
}

func t09TypedWantReceipt(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	receipt, err := ethguard.DecodeReceipt(t09SemanticJSON(t, raw))
	if err != nil {
		t.Fatalf("decode expected receipt: %v", err)
	}
	return t09SummaryReceipt(receipt)
}

func t09TypedWantReceipts(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	receipts, err := ethguard.DecodeReceipts(t09SemanticJSON(t, raw))
	if err != nil {
		t.Fatalf("decode expected receipts: %v", err)
	}
	return t09SummaryReceipts(receipts)
}

func t09TypedWantTransaction(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	tx, pending, err := ethguard.DecodeTransaction(t09SemanticJSON(t, raw))
	if err != nil {
		t.Fatalf("decode expected transaction: %v", err)
	}
	return t09SummaryTransaction(tx, pending)
}

func t09TypedWantLogs(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	logs, err := typed.DecodeLogs(t09SemanticJSON(t, raw))
	if err != nil {
		t.Fatalf("decode expected logs: %v", err)
	}
	return t09SummaryLogs(logs)
}

// typedTransactionBridge converts a public generated transaction union back
// to a geth transaction through its canonical JSON, so tests can recompute
// the signed-payload hash and compare against geth-side fixtures.
func typedTransactionBridge(t *testing.T, tx *ethapi.Transaction) *types.Transaction {
	t.Helper()
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal typed transaction: %v", err)
	}
	var out types.Transaction
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("bridge typed transaction: %v", err)
	}
	return &out
}

type t09FeeWantWire struct {
	OldestBlock   *hexutil.Big     `json:"oldestBlock"`
	BaseFeePerGas []*hexutil.Big   `json:"baseFeePerGas"`
	GasUsedRatio  []float64        `json:"gasUsedRatio"`
	Reward        [][]*hexutil.Big `json:"reward"`
}

func t09TypedWantFee(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var wire t09FeeWantWire
	if err := json.Unmarshal(t09SemanticJSON(t, raw), &wire); err != nil {
		t.Fatalf("decode expected fee history: %v", err)
	}
	return t09FeeWireSummary(wire)
}

func t09TypedWantAccess(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var wire struct {
		AccessList *types.AccessList `json:"accessList,omitempty"`
		Error      *string           `json:"error,omitempty"`
		GasUsed    *hexutil.Uint64   `json:"gasUsed,omitempty"`
	}
	if err := json.Unmarshal(t09SemanticJSON(t, raw), &wire); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func t09TypedWantSync(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if string(t09SemanticJSON(t, raw)) != "false" {
		t.Fatalf("syncing expectation must be the legal false state, got %s", trimValue(raw))
	}
	return "false"
}

// Typed probes call production typed APIs. Adapter methods without a decision
// return are paired with the root call so one row checks both the decision and
// the public typed result.

func t09TypedChainID(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.ChainID(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedBlockByHash(t *testing.T, f *t09Fleet, params []any) (string, error) {
	block, _, err := f.client.BlockByHash(f.context(t), ethapi.Hash(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryBlock(block), nil
}

func t09TypedBlockByNumber(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if strings.EqualFold(t09ParamString(t, params, 0), "pending") {
		if _, _, err := f.client.BlockByNumber(ctx, big.NewInt(-1)); !errors.Is(err, ethguard.ErrInvalidRequest) {
			return "", fmt.Errorf("pending adapter boundary = %v, want invalid request", err)
		}
		raw, err := callIntoJSON(ctx, f.client, methods.MethodGetBlockByNumber, params...)
		if err != nil {
			return "", err
		}
		header, err := ethguard.DecodeBlockHeaderOnly(raw)
		if err != nil {
			return "", err
		}
		return t09SummaryHeader(header), nil
	}
	block, _, err := f.client.BlockByNumber(ctx, new(big.Int).SetUint64(t09ParamUint64(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryBlock(block), nil
}

func t09TypedBalance(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		raw, err := callIntoJSON(ctx, f.client, methods.MethodGetBalance, params...)
		if err != nil {
			return "", err
		}
		var value hexutil.Big
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return t09SummaryBig((*big.Int)(&value)), nil
	}
	value, _, err := f.client.BalanceAt(ctx, ethapi.Address(t09ParamString(t, params, 0)), new(big.Int).SetUint64(t09ParamUint64(t, params, 1)))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedReceipt(t *testing.T, f *t09Fleet, params []any) (string, error) {
	receipt, _, err := f.client.TransactionReceipt(f.context(t), ethapi.Hash(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryReceipt(receipt), nil
}

func t09TypedTransaction(t *testing.T, f *t09Fleet, params []any) (string, error) {
	tx, pending, _, err := f.client.TransactionByHash(f.context(t), ethapi.Hash(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryTransaction(tx, pending), nil
}

func t09TypedBlockReceipts(t *testing.T, f *t09Fleet, params []any) (string, error) {
	receipts, _, err := f.client.BlockReceipts(f.context(t), blockHashRef(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryReceipts(receipts), nil
}

func t09TypedNonce(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		raw, err := callIntoJSON(ctx, f.client, methods.MethodGetTransactionCount, params...)
		if err != nil {
			return "", err
		}
		var value hexutil.Uint64
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return t09SummaryQuantity(uint64(value)), nil
	}
	value, _, err := f.client.NonceAt(ctx, ethapi.Address(t09ParamString(t, params, 0)), new(big.Int).SetUint64(t09ParamUint64(t, params, 1)))
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(value), nil
}

func t09TypedTransactionCount(t *testing.T, f *t09Fleet, params []any) (string, error) {
	value, _, err := f.client.TransactionCount(f.context(t), ethapi.Hash(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(uint64(value)), nil
}

func t09TypedPendingTransactionCount(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.PendingTransactionCount(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(uint64(value)), nil
}

func t09TypedTransactionInBlock(t *testing.T, f *t09Fleet, params []any) (string, error) {
	tx, _, err := f.client.TransactionInBlock(f.context(t), ethapi.Hash(t09ParamString(t, params, 0)), uint(t09ParamUint64(t, params, 1)))
	if err != nil {
		return "", err
	}
	return t09SummaryTransaction(tx, false), nil
}

func t09TypedStorage(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 2) {
		raw, err := callIntoJSON(ctx, f.client, methods.MethodGetStorageAt, params...)
		if err != nil {
			return "", err
		}
		var value hexutil.Bytes
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return t09SummaryBytes(value), nil
	}
	value, _, err := f.client.StorageAt(ctx, ethapi.Address(t09ParamString(t, params, 0)),
		ethapi.Hash(t09ParamString(t, params, 1)), new(big.Int).SetUint64(t09ParamUint64(t, params, 2)))
	if err != nil {
		return "", err
	}
	return t09SummaryBytes(value), nil
}

func t09TypedCode(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		raw, err := callIntoJSON(ctx, f.client, methods.MethodGetCode, params...)
		if err != nil {
			return "", err
		}
		var value hexutil.Bytes
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return t09SummaryBytes(value), nil
	}
	value, _, err := f.client.CodeAt(ctx, ethapi.Address(t09ParamString(t, params, 0)), new(big.Int).SetUint64(t09ParamUint64(t, params, 1)))
	if err != nil {
		return "", err
	}
	return t09SummaryBytes(value), nil
}

func t09TypedCall(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		raw, err := callIntoJSON(ctx, f.client, methods.MethodCall, params...)
		if err != nil {
			return "", err
		}
		var value hexutil.Bytes
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return t09SummaryBytes(value), nil
	}
	to := common.HexToAddress(t09Address)
	value, _, err := f.client.CallContract(ctx, ethapitest.Call(ethereum.CallMsg{To: &to}), new(big.Int).SetUint64(t09ParamUint64(t, params, 1)))
	if err != nil {
		return "", err
	}
	return t09SummaryBytes(value), nil
}

func t09TypedEstimateGas(t *testing.T, f *t09Fleet, params []any) (string, error) {
	raw, err := callIntoJSON(f.context(t), f.client, methods.MethodEstimateGas, params...)
	if err != nil {
		return "", err
	}
	var value hexutil.Uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return t09SummaryQuantity(uint64(value)), nil
}

func t09TypedLogs(t *testing.T, f *t09Fleet, params []any) (string, error) {
	object, ok := params[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("logs probe: unsupported params %#v", params)
	}
	blockHash, ok := object["blockHash"].(string)
	if !ok {
		return "", fmt.Errorf("logs probe: missing blockHash in params %#v", params)
	}
	q, err := ethapi.NewFilterQuery(json.RawMessage(fmt.Sprintf(`{"blockHash":%q}`, blockHash)))
	if err != nil {
		return "", err
	}
	logs, _, err := f.client.FilterLogs(f.context(t), q)
	if err != nil {
		return "", err
	}
	return t09SummaryLogs(logs), nil
}

func t09TypedFeeHistory(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		raw, err := callIntoJSON(ctx, f.client, methods.MethodFeeHistory, params...)
		if err != nil {
			return "", err
		}
		var wire t09FeeWantWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			return "", err
		}
		return t09FeeWireSummary(wire), nil
	}
	fee, _, err := f.client.FeeHistory(ctx, t09ParamUint64(t, params, 0), new(big.Int).SetUint64(t09ParamUint64(t, params, 1)), []float64{50})
	if err != nil {
		return "", err
	}
	return t09SummaryFee(fee), nil
}

func t09FeeWireSummary(wire t09FeeWantWire) string {
	out := struct {
		OldestBlock  string
		BaseFee      []string
		GasUsedRatio []float64
		Reward       [][]string
	}{OldestBlock: (*big.Int)(wire.OldestBlock).String(), GasUsedRatio: wire.GasUsedRatio}
	for _, fee := range wire.BaseFeePerGas {
		out.BaseFee = append(out.BaseFee, (*big.Int)(fee).String())
	}
	for _, row := range wire.Reward {
		var rewards []string
		for _, reward := range row {
			rewards = append(rewards, (*big.Int)(reward).String())
		}
		out.Reward = append(out.Reward, rewards)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09TypedGasPrice(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.SuggestGasPrice(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedMaxPriorityFee(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.SuggestGasTipCap(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedBlobBaseFee(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.BlobBaseFee(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedBlockNumber(t *testing.T, f *t09Fleet, params []any) (string, error) {
	raw, err := callIntoJSON(f.context(t), f.client, methods.MethodBlockNumber, params...)
	if err != nil {
		return "", err
	}
	var value hexutil.Uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return t09SummaryQuantity(uint64(value)), nil
}

func t09TypedAccessList(t *testing.T, f *t09Fleet, params []any) (string, error) {
	result, _, err := f.client.CreateAccessList(f.context(t), params[0], params[1:]...)
	if err != nil {
		return "", err
	}
	return t09SummaryAccess(result), nil
}

func t09TypedPeerCount(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.PeerCount(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(value), nil
}

func t09TypedSyncing(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	progress, _, err := f.client.SyncProgress(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummarySync(progress), nil
}

func t09TypedNetVersion(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.NetworkID(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedListening(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	decision, err := callDecision(f.client.Call(f.context(t), methods.MethodListening))
	if err != nil {
		return "", err
	}
	var value bool
	if err := json.Unmarshal(decision.Value, &value); err != nil {
		return "", err
	}
	return fmt.Sprint(value), nil
}

func t09ParamString(t *testing.T, params []any, index int) string {
	t.Helper()
	value, ok := params[index].(string)
	if !ok {
		t.Fatalf("param %d = %#v, want string", index, params[index])
	}
	return value
}

func t09ParamUint64(t *testing.T, params []any, index int) uint64 {
	t.Helper()
	switch value := params[index].(type) {
	case uint:
		return uint64(value)
	case uint64:
		return value
	case uint32:
		return uint64(value)
	case uint16:
		return uint64(value)
	case uint8:
		return uint64(value)
	case string:
		var quantity hexutil.Uint64
		if err := quantity.UnmarshalText([]byte(value)); err != nil {
			t.Fatalf("param %d = %q: %v", index, value, err)
		}
		return uint64(quantity)
	default:
		t.Fatalf("param %d = %#v, want quantity", index, params[index])
	}
	return 0
}

func t09ParamIsPending(t *testing.T, params []any, index int) bool {
	t.Helper()
	value, ok := params[index].(string)
	return ok && strings.EqualFold(value, "pending")
}

func TestTypedProjectionPreservesFullUint64BlockNumbers(t *testing.T) {
	for _, height := range []uint64{1 << 63, math.MaxUint64} {
		block := hexutil.EncodeUint64(height)
		for _, tc := range []struct {
			method string
			probe  func(*testing.T, *t09Fleet, []any) (string, error)
			params []any
			index  int
		}{
			{methods.MethodGetBlockByNumber, t09TypedBlockByNumber, []any{block, true}, 0},
			{methods.MethodGetBalance, t09TypedBalance, []any{t09Address, block}, 1},
			{methods.MethodGetTransactionCount, t09TypedNonce, []any{t09Address, block}, 1},
			{methods.MethodGetStorageAt, t09TypedStorage, []any{t09Address, t09StorageKey, block}, 2},
			{methods.MethodGetCode, t09TypedCode, []any{t09Address, block}, 1},
			{methods.MethodCall, t09TypedCall, []any{map[string]any{"to": t09Address}, block}, 1},
			{methods.MethodFeeHistory, t09TypedFeeHistory, []any{uint64(2), block, []float64{50}}, 1},
		} {
			t.Run(tc.method+"/"+block, func(t *testing.T) {
				f := t09NewFleet(t, 1)
				// Stop at the provider response: this assertion concerns the
				// requested height, independent of each response's schema.
				f.nodes[0].Fail(tc.method, -32603, "quantity probe")
				if _, err := tc.probe(t, f, tc.params); err == nil {
					t.Fatal("scripted provider failure was lost")
				}
				calls := f.nodes[0].Calls()
				if len(calls) != 1 || calls[0].Method != tc.method {
					t.Fatalf("calls = %+v, want one %s request", calls, tc.method)
				}
				var got string
				if err := json.Unmarshal(calls[0].Params[tc.index], &got); err != nil {
					t.Fatal(err)
				}
				if got != block {
					t.Fatalf("block parameter = %q, want %q", got, block)
				}
			})
		}
	}
}

func TestTypedProjectionPreservesFullUint64FeeCount(t *testing.T) {
	for _, count := range []uint64{1 << 63, math.MaxUint64} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := t09NewFleet(t, 1)
			f.nodes[0].Fail(methods.MethodFeeHistory, -32603, "quantity probe")
			if _, err := t09TypedFeeHistory(t, f, []any{count, "0x10", []float64{50}}); err == nil {
				t.Fatal("scripted provider failure was lost")
			}
			calls := f.nodes[0].Calls()
			if len(calls) != 1 || calls[0].Method != methods.MethodFeeHistory {
				t.Fatalf("calls = %+v, want one fee history request", calls)
			}
			var got uint64
			if err := json.Unmarshal(calls[0].Params[0], &got); err != nil {
				t.Fatal(err)
			}
			if got != count {
				t.Fatalf("fee count = %d, want %d", got, count)
			}
		})
	}
}

package ethguard_test

// Production typed probes and summaries used by T09.2 round trips and the
// T09.1 matrix. There is no test-side typed decoder in these paths: every
// summary comes from the same ethguard Client or ethadapter result a caller
// receives.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// Typed summary helpers. Every summary is the production typed shape's own
// value; the corresponding TypedWant helper applies the production decoder to
// the scripted answer.

func t09SummaryBig(value *big.Int) string { return value.String() }

func t09SummaryQuantity(value uint64) string { return fmt.Sprint(value) }

func t09SummaryBytes(value []byte) string { return hexutil.Encode(value) }

func t09SummaryBlock(block *types.Block) string {
	summary, err := t09BlockSummary(block)
	if err != nil {
		panic(err)
	}
	return summary
}

func t09SummaryHeader(header *types.Header) string {
	raw, err := json.Marshal(header)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryReceipt(receipt *types.Receipt) string {
	raw, err := json.Marshal(receipt)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryReceipts(receipts []*types.Receipt) string {
	raw, err := json.Marshal(receipts)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryTransaction(tx *types.Transaction, pending bool) string {
	raw, err := json.Marshal(tx)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s|pending=%v", raw, pending)
}

func t09SummaryLogs(logs []types.Log) string {
	raw, err := json.Marshal(logs)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummaryFee(fee *ethguard.FeeHistory) string {
	raw, err := json.Marshal(struct {
		OldestBlock   string
		BaseFeePerGas []string
		GasUsedRatio  []float64
		Reward        [][]string
	}{
		OldestBlock:   fee.OldestBlock.String(),
		BaseFeePerGas: t09BigStrings(fee.BaseFeePerGas),
		GasUsedRatio:  fee.GasUsedRatio,
		Reward:        t09BigRows(fee.Reward),
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09BigStrings(values []*big.Int) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.String()
	}
	return out
}

func t09BigRows(values [][]*big.Int) [][]string {
	out := make([][]string, len(values))
	for i, row := range values {
		out[i] = t09BigStrings(row)
	}
	return out
}

func t09SummaryAccess(result *ethguard.AccessListResult) string {
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func t09SummarySync(progress *ethereum.SyncProgress) string {
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
	block, err := ethguard.DecodeBlock(t09SemanticJSON(t, raw))
	if err != nil {
		t.Fatalf("decode expected block: %v", err)
	}
	return t09SummaryBlock(block)
}

func t09TypedWantHeader(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var header types.Header
	if err := json.Unmarshal(t09SemanticJSON(t, raw), &header); err != nil {
		t.Fatalf("decode expected header: %v", err)
	}
	return t09SummaryHeader(&header)
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
	var logs []types.Log
	if err := json.Unmarshal(t09SemanticJSON(t, raw), &logs); err != nil {
		t.Fatalf("decode expected logs: %v", err)
	}
	return t09SummaryLogs(logs)
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
		AccessList types.AccessList `json:"accessList"`
		GasUsed    hexutil.Uint64   `json:"gasUsed"`
		Error      string           `json:"error"`
	}
	if err := json.Unmarshal(t09SemanticJSON(t, raw), &wire); err != nil {
		t.Fatalf("decode expected access list: %v", err)
	}
	return t09SummaryAccess(&ethguard.AccessListResult{
		AccessList: wire.AccessList,
		GasUsed:    uint64(wire.GasUsed),
		Error:      wire.Error,
	})
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
	block, _, err := f.client.BlockByHash(f.context(t), common.HexToHash(t09ParamString(t, params, 0)), true)
	if err != nil {
		return "", err
	}
	return t09SummaryBlock(block), nil
}

func t09TypedBlockByNumber(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if strings.EqualFold(t09ParamString(t, params, 0), "pending") {
		if _, err := f.adapter.BlockByNumber(ctx, big.NewInt(-1)); !errors.Is(err, ethguard.ErrInvalidRequest) {
			return "", fmt.Errorf("pending adapter boundary = %v, want invalid request", err)
		}
		header, _, err := ethguard.CallInto[types.Header](ctx, f.client, methods.MethodGetBlockByNumber, params...)
		if err != nil {
			return "", err
		}
		return t09SummaryHeader(header), nil
	}
	block, err := f.adapter.BlockByNumber(ctx, big.NewInt(int64(t09ParamUint64(t, params, 0))))
	if err != nil {
		return "", err
	}
	return t09SummaryBlock(block), nil
}

func t09TypedBalance(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		value, _, err := ethguard.CallInto[hexutil.Big](ctx, f.client, methods.MethodGetBalance, params...)
		if err != nil {
			return "", err
		}
		return t09SummaryBig((*big.Int)(value)), nil
	}
	value, err := f.adapter.BalanceAt(ctx, common.HexToAddress(t09ParamString(t, params, 0)), big.NewInt(int64(t09ParamUint64(t, params, 1))))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedReceipt(t *testing.T, f *t09Fleet, params []any) (string, error) {
	receipt, err := f.adapter.TransactionReceipt(f.context(t), common.HexToHash(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryReceipt(receipt), nil
}

func t09TypedTransaction(t *testing.T, f *t09Fleet, params []any) (string, error) {
	tx, pending, err := f.adapter.TransactionByHash(f.context(t), common.HexToHash(t09ParamString(t, params, 0)))
	if err != nil {
		return "", err
	}
	return t09SummaryTransaction(tx, pending), nil
}

func t09TypedBlockReceipts(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ref := gethrpc.BlockNumberOrHashWithHash(common.HexToHash(t09ParamString(t, params, 0)), false)
	receipts, err := f.adapter.BlockReceipts(f.context(t), ref)
	if err != nil {
		return "", err
	}
	return t09SummaryReceipts(receipts), nil
}

func t09TypedNonce(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		value, _, err := ethguard.CallInto[hexutil.Uint64](ctx, f.client, methods.MethodGetTransactionCount, params...)
		if err != nil {
			return "", err
		}
		return t09SummaryQuantity(uint64(*value)), nil
	}
	value, err := f.adapter.NonceAt(ctx, common.HexToAddress(t09ParamString(t, params, 0)), big.NewInt(int64(t09ParamUint64(t, params, 1))))
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(value), nil
}

func t09TypedStorage(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 2) {
		value, _, err := ethguard.CallInto[hexutil.Bytes](ctx, f.client, methods.MethodGetStorageAt, params...)
		if err != nil {
			return "", err
		}
		return t09SummaryBytes(*value), nil
	}
	value, err := f.adapter.StorageAt(ctx, common.HexToAddress(t09ParamString(t, params, 0)),
		common.HexToHash(t09ParamString(t, params, 1)), big.NewInt(int64(t09ParamUint64(t, params, 2))))
	if err != nil {
		return "", err
	}
	return t09SummaryBytes(value), nil
}

func t09TypedCode(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		value, _, err := ethguard.CallInto[hexutil.Bytes](ctx, f.client, methods.MethodGetCode, params...)
		if err != nil {
			return "", err
		}
		return t09SummaryBytes(*value), nil
	}
	value, err := f.adapter.CodeAt(ctx, common.HexToAddress(t09ParamString(t, params, 0)), big.NewInt(int64(t09ParamUint64(t, params, 1))))
	if err != nil {
		return "", err
	}
	return t09SummaryBytes(value), nil
}

func t09TypedCall(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		value, _, err := ethguard.CallInto[hexutil.Bytes](ctx, f.client, methods.MethodCall, params...)
		if err != nil {
			return "", err
		}
		return t09SummaryBytes(*value), nil
	}
	to := common.HexToAddress(t09Address)
	value, err := f.adapter.CallContract(ctx, ethereum.CallMsg{To: &to}, big.NewInt(int64(t09ParamUint64(t, params, 1))))
	if err != nil {
		return "", err
	}
	return t09SummaryBytes(value), nil
}

func t09TypedEstimateGas(t *testing.T, f *t09Fleet, params []any) (string, error) {
	value, _, err := ethguard.CallInto[hexutil.Uint64](f.context(t), f.client, methods.MethodEstimateGas, params...)
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(uint64(*value)), nil
}

func t09TypedLogs(t *testing.T, f *t09Fleet, params []any) (string, error) {
	object, ok := params[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("logs probe: unsupported params %#v", params)
	}
	hash := common.HexToHash(object["blockHash"].(string))
	q := ethereum.FilterQuery{BlockHash: &hash}
	logs, err := f.adapter.FilterLogs(f.context(t), q)
	if err != nil {
		return "", err
	}
	return t09SummaryLogs(logs), nil
}

func t09TypedFeeHistory(t *testing.T, f *t09Fleet, params []any) (string, error) {
	ctx := f.context(t)
	if t09ParamIsPending(t, params, 1) {
		wire, _, err := ethguard.CallInto[t09FeeWantWire](ctx, f.client, methods.MethodFeeHistory, params...)
		if err != nil {
			return "", err
		}
		return t09FeeWireSummary(*wire), nil
	}
	fee, _, err := f.client.FeeHistory(ctx, uint64(t09ParamInt(t, params, 0)), big.NewInt(int64(t09ParamUint64(t, params, 1))), []float64{50})
	if err != nil {
		return "", err
	}
	return t09SummaryFee(fee), nil
}

func t09FeeWireSummary(wire t09FeeWantWire) string {
	out := struct {
		OldestBlock   string
		BaseFeePerGas []string
		GasUsedRatio  []float64
		Reward        [][]string
	}{OldestBlock: (*big.Int)(wire.OldestBlock).String(), GasUsedRatio: wire.GasUsedRatio}
	for _, fee := range wire.BaseFeePerGas {
		out.BaseFeePerGas = append(out.BaseFeePerGas, (*big.Int)(fee).String())
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
	value, _, err := f.client.GasPrice(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedMaxPriorityFee(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.MaxPriorityFeePerGas(f.context(t))
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
	value, _, err := ethguard.CallInto[hexutil.Uint64](f.context(t), f.client, methods.MethodBlockNumber, params...)
	if err != nil {
		return "", err
	}
	return t09SummaryQuantity(uint64(*value)), nil
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
	progress, _, err := f.client.Syncing(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummarySync(progress), nil
}

func t09TypedNetVersion(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.NetVersion(f.context(t))
	if err != nil {
		return "", err
	}
	return t09SummaryBig(value), nil
}

func t09TypedListening(t *testing.T, f *t09Fleet, _ []any) (string, error) {
	value, _, err := f.client.Listening(f.context(t))
	if err != nil {
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
	text := t09ParamString(t, params, index)
	var value hexutil.Uint64
	if err := value.UnmarshalText([]byte(text)); err != nil {
		t.Fatalf("param %d = %q: %v", index, text, err)
	}
	return uint64(value)
}

func t09ParamInt(t *testing.T, params []any, index int) int {
	t.Helper()
	value, ok := params[index].(uint64)
	if !ok {
		t.Fatalf("param %d = %#v, want uint64", index, params[index])
	}
	return int(value)
}

func t09ParamIsPending(t *testing.T, params []any, index int) bool {
	t.Helper()
	value, ok := params[index].(string)
	return ok && strings.EqualFold(value, "pending")
}

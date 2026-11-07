package typed

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/rpc"
)

func TestDecodeFeeHistoryProjectsGethShape(t *testing.T) {
	raw := json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x3"]]}`)
	history, err := DecodeFeeHistory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if history.OldestBlock.Int64() != 16 {
		t.Fatalf("oldest block = %v, want 16", history.OldestBlock)
	}
	if len(history.BaseFee) != 2 || history.BaseFee[0].Int64() != 1 || history.BaseFee[1].Int64() != 2 {
		t.Fatalf("base fee = %v", history.BaseFee)
	}
	if len(history.Reward) != 1 || len(history.Reward[0]) != 1 || history.Reward[0][0].Int64() != 3 {
		t.Fatalf("reward = %v", history.Reward)
	}
	if len(history.GasUsedRatio) != 1 || history.GasUsedRatio[0] != 0.5 {
		t.Fatalf("gas used ratio = %v", history.GasUsedRatio)
	}
}

func TestDecodeFeeHistoryMalformedClasses(t *testing.T) {
	if _, err := DecodeFeeHistory(json.RawMessage(`{"baseFeePerGas":[]}`)); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "no oldest block") {
		t.Fatalf("missing oldest block must fail malformed, got %v", err)
	}
	if _, err := DecodeFeeHistory(json.RawMessage(`["not an object"]`)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("shape mismatch must fail malformed, got %v", err)
	}
}

func TestDecodeGasEstimate(t *testing.T) {
	gas, err := DecodeGasEstimate(json.RawMessage(`"0x5208"`))
	if err != nil || gas != 21000 {
		t.Fatalf("gas = %d err = %v, want 21000", gas, err)
	}
	if _, err := DecodeGasEstimate(json.RawMessage(`"21"`)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("decimal spelling must fail malformed, got %v", err)
	}
	for _, raw := range []string{"null", " null ", "\tnull\n"} {
		if gas, err := DecodeGasEstimate(json.RawMessage(raw)); !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("JSON null (%q) must fail malformed, got gas=%d err=%v", raw, gas, err)
		}
	}
	if _, err := DecodeGasEstimate(json.RawMessage(`"0x10000000000000000"`)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("overflow quantity must fail malformed, got %v", err)
	}
}

func TestDecodeLogsNullIsMalformedNot(t *testing.T) {
	for _, spelling := range []string{"null", "  null  ", "\tnull\n"} {
		if logs, err := DecodeLogs(json.RawMessage(spelling)); err == nil || !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("null array (%q) must fail malformed, got logs=%v err=%v", spelling, logs, err)
		}
	}
	logs, err := DecodeLogs(json.RawMessage(`[]`))
	if err != nil || logs == nil || len(logs) != 0 {
		t.Fatalf("empty array must stay an empty non-nil slice, got %v err=%v", logs, err)
	}
}

func TestDecodeAccessListResultHexGas(t *testing.T) {
	raw := json.RawMessage(`{"accessList":[{"address":"` + common.HexToAddress("0x1").Hex() + `","storageKeys":[]}],"gasUsed":"0x5208"}`)
	result, err := DecodeAccessListResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.GasUsed != 21000 || len(result.AccessList) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := DecodeAccessListResult(json.RawMessage(`{"gasUsed":21}`)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("decimal gas must fail malformed, got %v", err)
	}
	if _, err := DecodeAccessListResult(json.RawMessage(` null `)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("null access list must fail malformed, got %v", err)
	}
}

func TestDecodeSyncProgressShapes(t *testing.T) {
	if progress, err := DecodeSyncProgress(json.RawMessage(`false`)); err != nil || progress != nil {
		t.Fatalf("false must project to nil progress, got %v err=%v", progress, err)
	}
	if _, err := DecodeSyncProgress(json.RawMessage(`true`)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("true must fail malformed, got %v", err)
	}
	high := hexutil.Uint64(64)
	raw, err := json.Marshal(syncProgressWire{StartingBlock: 1, CurrentBlock: 2, HighestBlock: 3, PulledStates: &high})
	if err != nil {
		t.Fatal(err)
	}
	progress, err := DecodeSyncProgress(raw)
	if err != nil {
		t.Fatal(err)
	}
	if progress.StartingBlock != 1 || progress.CurrentBlock != 2 || progress.HighestBlock != 3 || progress.PulledStates != 64 {
		t.Fatalf("progress = %+v", progress)
	}
	// Absent optional fields decode to zero, and JSON null is not a legal
	// syncing shape.
	if progress.SyncedAccountBytes != 0 || progress.HealingBytecode != 0 {
		t.Fatalf("absent optional fields = %+v", progress)
	}
	if _, err := DecodeSyncProgress(json.RawMessage(` null `)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("null syncing must fail malformed, got %v", err)
	}
}

// TestFacadeDecodersKeepTypeIdentity pins that the re-exported
// AccessListResult is the typed package's type, not a copy.
func TestFacadeDecodersKeepTypeIdentity(t *testing.T) {
	result, err := DecodeAccessListResult(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var _ *AccessListResult = result
	var _ types.AccessList = result.AccessList
	if new(big.Int).Sign() != 0 {
		t.Fatal("unreachable")
	}
}

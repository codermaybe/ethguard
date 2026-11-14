package typed

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

func TestDecodeFeeHistoryPreservesGeneratedShape(t *testing.T) {
	raw := json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x3"]]}`)
	history, err := DecodeFeeHistory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if history.OldestBlock != "0x10" {
		t.Fatalf("oldest block = %v, want 16", history.OldestBlock)
	}
	if len(history.BaseFeePerGas) != 2 || history.BaseFeePerGas[0] != "0x1" || history.BaseFeePerGas[1] != "0x2" {
		t.Fatalf("base fee = %v", history.BaseFeePerGas)
	}
	if history.Reward == nil || len(*history.Reward) != 1 || len((*history.Reward)[0]) != 1 || (*history.Reward)[0][0] != "0x3" {
		t.Fatalf("reward = %v", history.Reward)
	}
	if len(history.GasUsedRatio) != 1 || history.GasUsedRatio[0] != "0.5" {
		t.Fatalf("gas used ratio = %v", history.GasUsedRatio)
	}
}

func TestDecodeFeeHistoryMalformedClasses(t *testing.T) {
	if _, err := DecodeFeeHistory(json.RawMessage(`{"baseFeePerGas":[]}`)); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "missing required field oldestBlock") {
		t.Fatalf("missing oldest block must fail malformed, got %v", err)
	}
	if _, err := DecodeFeeHistory(json.RawMessage(`["not an object"]`)); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("shape mismatch must fail malformed, got %v", err)
	}
}

func TestDecodeFeeHistoryPreservesBlobFieldsAndExactNumbers(t *testing.T) {
	raw := json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.30000000000000001],"baseFeePerBlobGas":["0x3","0x4"],"blobGasUsedRatio":[0.10000000000000001]}`)
	history, err := DecodeFeeHistory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if history.GasUsedRatio[0] != "0.30000000000000001" || history.Reward != nil ||
		history.BaseFeePerBlobGas == nil || (*history.BaseFeePerBlobGas)[1] != "0x4" ||
		history.BlobGasUsedRatio == nil || (*history.BlobGasUsedRatio)[0] != "0.10000000000000001" {
		t.Fatalf("generated fee history lost values or presence: %+v", history)
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
	if result.GasUsed == nil || *result.GasUsed != "0x5208" || result.AccessList == nil || len(*result.AccessList) != 1 {
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
		t.Fatalf("false = %+v, %v", progress, err)
	}
	for _, raw := range []string{"true", "null", "[]", "1"} {
		if _, err := DecodeSyncProgress(json.RawMessage(raw)); !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("%s accepted: %v", raw, err)
		}
	}
	raw := json.RawMessage(`{"startingBlock":"0x1","currentBlock":"0x2","highestBlock":"0x3","pulledStates":"0x40","trienodeIndexRemaining":"0x100000017"}`)
	progress, err := DecodeSyncProgress(raw)
	if err != nil {
		t.Fatal(err)
	}
	if progress.StartingBlock == nil || *progress.StartingBlock != "0x1" || progress.CurrentBlock == nil || *progress.CurrentBlock != "0x2" || progress.HighestBlock == nil || *progress.HighestBlock != "0x3" {
		t.Fatalf("official core = %+v", progress)
	}
	out, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "pulledStates") || strings.Contains(string(out), "trienodeIndexRemaining") {
		t.Fatalf("provider counters mixed into official core: %s", out)
	}
}

// The decoder returns the public generated type itself, including absence.
func TestFacadeDecodersKeepGeneratedTypeIdentity(t *testing.T) {
	result, err := DecodeAccessListResult(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var _ *ethapi.AccessListResult = result
	if result.AccessList != nil || result.GasUsed != nil || result.Error != nil {
		t.Fatalf("missing fields lost absence: %+v", result)
	}
}

// The remaining per-method typed projections: every decoder here owns the
// wire-to-value shape for exactly one method's resolved value. There is no
// universal decoder by design — null, pending, NotFound and malformed
// semantics stay method-specific and visible at each entry point.
package typed

import (
	"bytes"
	"encoding/json"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// jsonNull reports whether the raw value is JSON null (whitespace tolerated).
// A method answer that is null is a shape question, not a decode default:
// decoders that own a null rule must see through surrounding whitespace, or a
// padded null would slip past the check and take the zero-value path.
func jsonNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// AccessListResult represents the outcome of eth_createAccessList. The root
// facade re-exports the type; the decode owns the hex gas spelling.
type AccessListResult struct {
	AccessList types.AccessList `json:"accessList"`
	GasUsed    uint64           `json:"gasUsed"`
	Error      string           `json:"error,omitempty"`
}

type accessListResultWire struct {
	AccessList types.AccessList `json:"accessList"`
	GasUsed    hexutil.Uint64   `json:"gasUsed"`
	Error      string           `json:"error,omitempty"`
}

// DecodeAccessListResult shapes one corroborated eth_createAccessList value.
// The method answers with an object, so JSON null fails malformed instead of
// collapsing into an empty result.
func DecodeAccessListResult(raw json.RawMessage) (*AccessListResult, error) {
	if jsonNull(raw) {
		return nil, wrapMalformed("decode access list result: JSON null", nil)
	}
	var wire accessListResultWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, wrapMalformed("decode access list result", err)
	}
	return &AccessListResult{
		AccessList: wire.AccessList,
		GasUsed:    uint64(wire.GasUsed),
		Error:      wire.Error,
	}, nil
}

// DecodeFeeHistory shapes one corroborated eth_feeHistory value into
// go-ethereum's own fee-history type. A payload without an oldest block
// cannot address its range and fails malformed even when unanimous.
func DecodeFeeHistory(raw json.RawMessage) (*ethereum.FeeHistory, error) {
	var wire struct {
		OldestBlock  *hexutil.Big     `json:"oldestBlock"`
		BaseFee      []*hexutil.Big   `json:"baseFeePerGas"`
		GasUsedRatio []float64        `json:"gasUsedRatio"`
		Reward       [][]*hexutil.Big `json:"reward"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, wrapMalformed("decode fee history", err)
	}
	if wire.OldestBlock == nil {
		return nil, wrapMalformed("fee history carries no oldest block", nil)
	}
	out := &ethereum.FeeHistory{
		OldestBlock:  (*big.Int)(wire.OldestBlock),
		BaseFee:      make([]*big.Int, 0, len(wire.BaseFee)),
		GasUsedRatio: wire.GasUsedRatio,
	}
	for _, fee := range wire.BaseFee {
		out.BaseFee = append(out.BaseFee, (*big.Int)(fee))
	}
	if wire.Reward != nil {
		out.Reward = make([][]*big.Int, 0, len(wire.Reward))
		for _, row := range wire.Reward {
			rewards := make([]*big.Int, 0, len(row))
			for _, reward := range row {
				rewards = append(rewards, (*big.Int)(reward))
			}
			out.Reward = append(out.Reward, rewards)
		}
	}
	return out, nil
}

// DecodeGasEstimate shapes one corroborated eth_estimateGas quantity.
func DecodeGasEstimate(raw json.RawMessage) (uint64, error) {
	if jsonNull(raw) {
		return 0, wrapMalformed("decode gas estimate: JSON null", nil)
	}
	var gas hexutil.Uint64
	if err := json.Unmarshal(raw, &gas); err != nil {
		return 0, wrapMalformed("decode gas estimate", err)
	}
	return uint64(gas), nil
}

// DecodeLogs shapes one corroborated eth_getLogs array. A JSON null payload
// is not an empty result: a log query answers with an array, so null —
// including whitespace-padded spellings, which the trim check sees through —
// fails malformed instead of collapsing into an empty slice.
func DecodeLogs(raw json.RawMessage) ([]types.Log, error) {
	if jsonNull(raw) {
		return nil, wrapMalformed("log array is JSON null", nil)
	}
	var logs []types.Log
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, wrapMalformed("decode logs", err)
	}
	if logs == nil {
		return nil, wrapMalformed("log array is JSON null", nil)
	}
	return logs, nil
}

// syncProgressWire decodes the progress fields currently projected by
// EthGuard; it is not the complete go-ethereum SyncProgress field set. Optional
// snap-era fields ride as pointers and stay zero when absent: a provider that
// does not report a byte-count or healing field yields zero, and no field is
// claimed beyond the wire's own evidence.
type syncProgressWire struct {
	StartingBlock       hexutil.Uint64  `json:"startingBlock"`
	CurrentBlock        hexutil.Uint64  `json:"currentBlock"`
	HighestBlock        hexutil.Uint64  `json:"highestBlock"`
	PulledStates        *hexutil.Uint64 `json:"pulledStates"`
	KnownStates         *hexutil.Uint64 `json:"knownStates"`
	SyncedAccounts      *hexutil.Uint64 `json:"syncedAccounts"`
	SyncedAccountBytes  *hexutil.Uint64 `json:"syncedAccountBytes"`
	SyncedBytecodes     *hexutil.Uint64 `json:"syncedBytecodes"`
	SyncedBytecodeBytes *hexutil.Uint64 `json:"syncedBytecodeBytes"`
	SyncedStorage       *hexutil.Uint64 `json:"syncedStorage"`
	SyncedStorageBytes  *hexutil.Uint64 `json:"syncedStorageBytes"`
	HealedTrienodes     *hexutil.Uint64 `json:"healedTrienodes"`
	HealedTrienodeBytes *hexutil.Uint64 `json:"healedTrienodeBytes"`
	HealedBytecodes     *hexutil.Uint64 `json:"healedBytecodes"`
	HealedBytecodeBytes *hexutil.Uint64 `json:"healedBytecodeBytes"`
	HealingTrienodes    *hexutil.Uint64 `json:"healingTrienodes"`
	HealingBytecode     *hexutil.Uint64 `json:"healingBytecode"`
}

func (w syncProgressWire) toSyncProgress() *ethereum.SyncProgress {
	pick := func(v *hexutil.Uint64) uint64 {
		if v == nil {
			return 0
		}
		return uint64(*v)
	}
	return &ethereum.SyncProgress{
		StartingBlock:       uint64(w.StartingBlock),
		CurrentBlock:        uint64(w.CurrentBlock),
		HighestBlock:        uint64(w.HighestBlock),
		PulledStates:        pick(w.PulledStates),
		KnownStates:         pick(w.KnownStates),
		SyncedAccounts:      pick(w.SyncedAccounts),
		SyncedAccountBytes:  pick(w.SyncedAccountBytes),
		SyncedBytecodes:     pick(w.SyncedBytecodes),
		SyncedBytecodeBytes: pick(w.SyncedBytecodeBytes),
		SyncedStorage:       pick(w.SyncedStorage),
		SyncedStorageBytes:  pick(w.SyncedStorageBytes),
		HealedTrienodes:     pick(w.HealedTrienodes),
		HealedTrienodeBytes: pick(w.HealedTrienodeBytes),
		HealedBytecodes:     pick(w.HealedBytecodes),
		HealedBytecodeBytes: pick(w.HealedBytecodeBytes),
		HealingTrienodes:    pick(w.HealingTrienodes),
		HealingBytecode:     pick(w.HealingBytecode),
	}
}

// DecodeSyncProgress shapes one eth_syncing value: a false boolean means the
// source considers itself fully synced and projects to a nil progress; a
// true boolean and JSON null are not shapes sources return and fail
// malformed; the object form decodes through go-ethereum's field spelling.
func DecodeSyncProgress(raw json.RawMessage) (*ethereum.SyncProgress, error) {
	if jsonNull(raw) {
		return nil, wrapMalformed("decode sync progress: JSON null", nil)
	}
	var flag bool
	if json.Unmarshal(raw, &flag) == nil {
		if !flag {
			return nil, nil
		}
		return nil, wrapMalformed("syncing boolean true is not a shape sources return", nil)
	}
	var wire syncProgressWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, wrapMalformed("decode sync progress", err)
	}
	return wire.toSyncProgress(), nil
}

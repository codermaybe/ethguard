// The remaining per-method typed projections: every decoder here owns the
// wire-to-value shape for exactly one method's resolved value. There is no
// universal decoder by design — null, pending, NotFound and malformed
// semantics stay method-specific and visible at each entry point.
package typed

import (
	"encoding/json"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

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
func DecodeAccessListResult(raw json.RawMessage) (*AccessListResult, error) {
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
	var gas hexutil.Uint64
	if err := json.Unmarshal(raw, &gas); err != nil {
		return 0, wrapMalformed("decode gas estimate", err)
	}
	return uint64(gas), nil
}

// DecodeLogs shapes one corroborated eth_getLogs array. A JSON null payload
// is not an empty result: a log query answers with an array, so null fails
// malformed instead of collapsing into an empty slice.
func DecodeLogs(raw json.RawMessage) ([]types.Log, error) {
	if string(raw) == "null" {
		return nil, wrapMalformed("log array is JSON null", nil)
	}
	var logs []types.Log
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, wrapMalformed("decode logs", err)
	}
	return logs, nil
}

// syncProgressWire mirrors go-ethereum's own client-side decoding of the
// eth_syncing object form; the snap-era optional fields ride as pointers and
// stay zero when absent.
type syncProgressWire struct {
	StartingBlock   hexutil.Uint64  `json:"startingBlock"`
	CurrentBlock    hexutil.Uint64  `json:"currentBlock"`
	HighestBlock    hexutil.Uint64  `json:"highestBlock"`
	PulledStates    *hexutil.Uint64 `json:"pulledStates"`
	KnownStates     *hexutil.Uint64 `json:"knownStates"`
	SyncedAccounts  *hexutil.Uint64 `json:"syncedAccounts"`
	SyncedBytecodes *hexutil.Uint64 `json:"syncedBytecodes"`
	SyncedStorage   *hexutil.Uint64 `json:"syncedStorage"`
	HealedTrienodes *hexutil.Uint64 `json:"healedTrienodes"`
	HealedBytecodes *hexutil.Uint64 `json:"healedBytecodes"`
}

func (w syncProgressWire) toSyncProgress() *ethereum.SyncProgress {
	pick := func(v *hexutil.Uint64) uint64 {
		if v == nil {
			return 0
		}
		return uint64(*v)
	}
	return &ethereum.SyncProgress{
		StartingBlock:   uint64(w.StartingBlock),
		CurrentBlock:    uint64(w.CurrentBlock),
		HighestBlock:    uint64(w.HighestBlock),
		PulledStates:    pick(w.PulledStates),
		KnownStates:     pick(w.KnownStates),
		SyncedAccounts:  pick(w.SyncedAccounts),
		SyncedBytecodes: pick(w.SyncedBytecodes),
		SyncedStorage:   pick(w.SyncedStorage),
		HealedTrienodes: pick(w.HealedTrienodes),
		HealedBytecodes: pick(w.HealedBytecodes),
	}
}

// DecodeSyncProgress shapes one eth_syncing value: a false boolean means the
// source considers itself fully synced and projects to a nil progress; a
// true boolean is not a shape sources return and fails malformed; the object
// form decodes through go-ethereum's field spelling.
func DecodeSyncProgress(raw json.RawMessage) (*ethereum.SyncProgress, error) {
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

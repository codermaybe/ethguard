package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// ChainID returns the verified chain ID and its aggregation decision.
func (c *Client) ChainID(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodChainID)
	if err != nil {
		return nil, decision, err
	}
	// Hex parsing has one owner: the method policy. Convert its canonical
	// decimal form instead of re-parsing the raw quantity here.
	canonical, err := c.policies.PolicyFor(methods.MethodChainID).Normalize(decision.Value)
	if err != nil {
		return nil, decision, fmt.Errorf("ethguard: decode chain ID: %w", err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, decision, fmt.Errorf("ethguard: invalid chain ID %q", canonical)
	}
	return value, decision, nil
}

// BlockByHash returns a strictly compared block and the aggregation decision.
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash, fullTx bool) (*types.Block, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGetBlockByHash, hash.Hex(), fullTx)
	if err != nil {
		return nil, decision, err
	}
	block, err := DecodeBlock(decision.Value)
	if err != nil {
		return nil, decision, err
	}
	return block, decision, nil
}

// GasPrice returns the fleet's lower-median gas price — a variable result,
// so the decision is Selected at best, with outliers kept as evidence.
func (c *Client) GasPrice(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGasPrice)
	if err != nil {
		return nil, decision, err
	}
	canonical, err := c.Normalize(methods.MethodGasPrice, decision.Value)
	if err != nil {
		return nil, decision, fmt.Errorf("ethguard: decode gas price: %w", err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, decision, fmt.Errorf("ethguard: invalid gas price %q", canonical)
	}
	return value, decision, nil
}

// PeerCount returns one healthy source's count of its own peers — an
// upstream-local result, selected not compared.
func (c *Client) PeerCount(ctx context.Context) (uint64, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodPeerCount)
	if err != nil {
		return 0, decision, err
	}
	canonical, err := c.Normalize(methods.MethodPeerCount, decision.Value)
	if err != nil {
		return 0, decision, fmt.Errorf("ethguard: decode peer count: %w", err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok || !value.IsUint64() {
		return 0, decision, fmt.Errorf("ethguard: invalid peer count %q", canonical)
	}
	return value.Uint64(), decision, nil
}

// Syncing reports the consulted source's sync state: nil progress means it
// considers itself fully synced. An upstream-local result, never verified.
func (c *Client) Syncing(ctx context.Context) (*ethereum.SyncProgress, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodSyncing)
	if err != nil {
		return nil, decision, err
	}
	var flag bool
	if json.Unmarshal(decision.Value, &flag) == nil {
		if !flag {
			return nil, decision, nil
		}
		return nil, decision, fmt.Errorf("%w: syncing boolean true is not a shape sources return",
			rpc.ErrMalformedResponse)
	}
	var wire syncProgressWire
	if err := json.Unmarshal(decision.Value, &wire); err != nil {
		return nil, decision, fmt.Errorf("%w: decode sync progress: %w", rpc.ErrMalformedResponse, err)
	}
	return wire.toSyncProgress(), decision, nil
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

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

// TransactionReceipt returns a strictly compared receipt for the transaction
// hash and the aggregation decision. The hash addresses immutable content:
// honest sources either hold the identical receipt or the dissent is
// evidence. Verified absence — every source answers null, the transaction
// has no receipt yet — maps to ethereum.NotFound, the convention DecodeBlock
// applies to blocks and go-ethereum's own client applies to receipts.
func (c *Client) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGetTransactionReceipt, hash.Hex())
	if err != nil {
		return nil, decision, err
	}
	receipt, err := DecodeReceipt(decision.Value)
	if err != nil {
		return nil, decision, err
	}
	return receipt, decision, nil
}

// TransactionByHash returns a strictly compared transaction and the
// aggregation decision. The hash addresses immutable content; verified
// absence maps to ethereum.NotFound like TransactionReceipt.
func (c *Client) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGetTransactionByHash, hash.Hex())
	if err != nil {
		return nil, decision, err
	}
	tx, _, err := DecodeTransaction(decision.Value)
	if err != nil {
		return nil, decision, err
	}
	return tx, decision, nil
}

// BlockReceipts returns the strictly compared receipts of one block,
// addressed by its verified hash. Moving tags never reach this method:
// anchor the reference first (Adapter callers pass numbers or tags and the
// policy rewrites them). Verified absence of the block maps to
// ethereum.NotFound; an empty block yields an empty, non-nil slice.
func (c *Client) BlockReceipts(ctx context.Context, hash common.Hash) ([]*types.Receipt, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGetBlockReceipts, hash.Hex())
	if err != nil {
		return nil, decision, err
	}
	receipts, err := DecodeReceipts(decision.Value)
	if err != nil {
		return nil, decision, err
	}
	return receipts, decision, nil
}

// MaxPriorityFeePerGas returns the fleet's lower-median priority-fee
// estimate — a variable result, Selected at best, with outliers kept as
// evidence (see GasPrice).
func (c *Client) MaxPriorityFeePerGas(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodMaxPriorityFeePerGas)
	if err != nil {
		return nil, decision, err
	}
	canonical, err := c.Normalize(methods.MethodMaxPriorityFeePerGas, decision.Value)
	if err != nil {
		return nil, decision, fmt.Errorf("ethguard: decode max priority fee: %w", err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, decision, fmt.Errorf("ethguard: invalid max priority fee %q", canonical)
	}
	return value, decision, nil
}

// NetVersion returns the verified legacy chain identity (net_version spells
// in decimal what eth_chainId spells in hex).
func (c *Client) NetVersion(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodNetVersion)
	if err != nil {
		return nil, decision, err
	}
	canonical, err := c.Normalize(methods.MethodNetVersion, decision.Value)
	if err != nil {
		return nil, decision, fmt.Errorf("ethguard: decode net version: %w", err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, decision, fmt.Errorf("ethguard: invalid net version %q", canonical)
	}
	return value, decision, nil
}

// Listening reports whether the consulted source accepts inbound peers —
// an upstream-local result, selected not compared.
func (c *Client) Listening(ctx context.Context) (bool, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodListening)
	if err != nil {
		return false, decision, err
	}
	var flag bool
	if err := json.Unmarshal(decision.Value, &flag); err != nil {
		return false, decision, fmt.Errorf("%w: decode listening flag: %w", rpc.ErrMalformedResponse, err)
	}
	return flag, decision, nil
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

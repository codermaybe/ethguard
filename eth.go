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

// decodedQuantity is the shared tail of every quantity-shaped typed wrapper:
// the owning policy's canonical decimal form converts to a big.Int. Hex
// parsing has one owner — the method policy — and typed callers convert its
// canonical form instead of re-parsing raw JSON.
func (c *Client) decodedQuantity(method string, decision rpc.Decision) (*big.Int, error) {
	canonical, err := c.Normalize(method, decision.Value)
	if err != nil {
		return nil, fmt.Errorf("ethguard: decode %s: %w", method, err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, fmt.Errorf("ethguard: invalid %s %q", method, canonical)
	}
	return value, nil
}

// ChainID returns the verified chain ID and its aggregation decision.
func (c *Client) ChainID(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodChainID)
	if err != nil {
		return nil, decision, err
	}
	value, err := c.decodedQuantity(methods.MethodChainID, decision)
	if err != nil {
		return nil, decision, err
	}
	return value, decision, nil
}

// BlockByHash returns a strictly compared block and the aggregation decision.
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash, fullTx bool) (*types.Block, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGetBlockByHash, hash.Hex(), fullTx)
	if err != nil {
		return nil, decision, err
	}
	var block *types.Block
	if fullTx {
		block, err = DecodeBlockWithEvidence(decision.Value, decision.Evidence)
	} else {
		block, err = DecodeBlockHeaderOnly(decision.Value)
	}
	if err != nil {
		return nil, decision, err
	}
	if block.Hash() != hash {
		return nil, decision, fmt.Errorf("%w: block hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, block.Hash(), hash)
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
	if receipt.TxHash != hash {
		return nil, decision, fmt.Errorf("%w: receipt transaction hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, receipt.TxHash, hash)
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
	if tx.Hash() != hash {
		return nil, decision, fmt.Errorf("%w: transaction hash %s does not match requested hash %s",
			rpc.ErrMalformedResponse, tx.Hash(), hash)
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
	for i, receipt := range receipts {
		if receipt.BlockHash != hash {
			return nil, decision, fmt.Errorf("%w: receipt %d block hash %s does not match requested hash %s",
				rpc.ErrMalformedResponse, i, receipt.BlockHash, hash)
		}
	}
	return receipts, decision, nil
}

// FeeHistory describes the fleet-verified fee history of a pinned range.
// It has no ethclient-shaped home, so it lives at the root (division rule:
// the adapter mirrors ethclient; go-ethereum's client does not expose
// feeHistory).
type FeeHistory struct {
	// OldestBlock is the first block of the returned range.
	OldestBlock *big.Int
	// BaseFeePerGas holds one entry per block of the range plus the
	// EIP-1559 prediction for the block after the newest.
	BaseFeePerGas []*big.Int
	// GasUsedRatio holds one gas-used/gas-limit ratio per block of the range.
	GasUsedRatio []float64
	// Reward holds, per block, the priority fees at the caller's requested
	// percentiles. Nil when no percentiles were requested.
	Reward [][]*big.Int
}

// FeeHistory returns the fleet-verified fee history ending at the verified
// reference: newest nil pins the moving "latest" tag, an explicit number
// passes through. The range is chain-determined once newest is pinned, so
// the decision is Verified when sources agree; cross-client percentile
// rounding of Reward is the known liveness gate and fails closed as
// conflict until the soak measures it.
func (c *Client) FeeHistory(ctx context.Context, blockCount uint64, newest *big.Int, rewardPercentiles []float64) (*FeeHistory, rpc.Decision, error) {
	newestRef := "latest"
	if newest != nil {
		newestRef = hexutil.EncodeBig(newest)
	}
	decision, err := c.Call(ctx, methods.MethodFeeHistory, blockCount, newestRef, rewardPercentiles)
	if err != nil {
		return nil, decision, err
	}
	var wire struct {
		OldestBlock   *hexutil.Big     `json:"oldestBlock"`
		BaseFeePerGas []*hexutil.Big   `json:"baseFeePerGas"`
		GasUsedRatio  []float64        `json:"gasUsedRatio"`
		Reward        [][]*hexutil.Big `json:"reward"`
	}
	if err := json.Unmarshal(decision.Value, &wire); err != nil {
		return nil, decision, fmt.Errorf("%w: decode fee history: %v", rpc.ErrMalformedResponse, err)
	}
	if wire.OldestBlock == nil {
		return nil, decision, fmt.Errorf("%w: fee history carries no oldest block", rpc.ErrMalformedResponse)
	}
	out := &FeeHistory{
		OldestBlock:   (*big.Int)(wire.OldestBlock),
		BaseFeePerGas: make([]*big.Int, 0, len(wire.BaseFeePerGas)),
		GasUsedRatio:  wire.GasUsedRatio,
	}
	for _, fee := range wire.BaseFeePerGas {
		out.BaseFeePerGas = append(out.BaseFeePerGas, (*big.Int)(fee))
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
	return out, decision, nil
}

// MaxPriorityFeePerGas returns the fleet's lower-median priority-fee
// estimate — a variable result, Selected at best, with outliers kept as
// evidence (see GasPrice).
func (c *Client) MaxPriorityFeePerGas(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodMaxPriorityFeePerGas)
	if err != nil {
		return nil, decision, err
	}
	value, err := c.decodedQuantity(methods.MethodMaxPriorityFeePerGas, decision)
	if err != nil {
		return nil, decision, err
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
	value, err := c.decodedQuantity(methods.MethodNetVersion, decision)
	if err != nil {
		return nil, decision, err
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

// BlockNumber returns the height of the currently verified block reference.
// It is a derivation over verified primitives, not an independent read, and
// the selection reasoning is stated here rather than left implicit:
//
//   - Sources sit at different heads as a matter of course. A fleet fan-out
//     of eth_blockNumber would turn routine replication lag into conflict;
//     divergence here is the common case, not an anomaly, so fail-closed
//     comparison is the wrong tool for this read.
//   - The verified reference behind moving-tag reads already carries the
//     fleet's agreement. Its height is the only height with a verification
//     claim behind it — any other answer would be one provider's tip.
//   - eth_blockNumber therefore stays unregistered on purpose: a raw
//     fan-out of it falls through to transparent Forwarded, never a
//     pretended Verified on a moving value.
//
// This is the consensus-override shape proxyd popularized, expressed as
// derivation. Callers wanting bounded staleness should configure the
// anchoring presets (WithFinalizedAnchoring) and, optionally, its cache.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	ref, err := c.AnchorBlockHash(ctx)
	if err != nil {
		return 0, err
	}
	// The height lookup rides eth_getBlockByHash even though a "height of
	// this hash" read feels like eth_getBlockByNumber: the first live soak
	// run caught public endpoints rejecting a 32-byte hash in the number
	// position ("hex number > 64 bits"), so hash lookups use the
	// hash-addressed method, full stop.
	decision, err := c.Call(ctx, methods.MethodGetBlockByHash, ref, true)
	if err != nil {
		return 0, err
	}
	block, err := DecodeBlockWithEvidence(decision.Value, decision.Evidence)
	if err != nil {
		return 0, err
	}
	if block.Hash() != common.HexToHash(ref) {
		return 0, fmt.Errorf("%w: block hash %s does not match anchor reference %s",
			rpc.ErrMalformedResponse, block.Hash(), ref)
	}
	return block.NumberU64(), nil
}

// GasPrice returns the fleet's lower-median gas price — a variable result,
// so the decision is Selected at best, with outliers kept as evidence.
func (c *Client) GasPrice(ctx context.Context) (*big.Int, rpc.Decision, error) {
	decision, err := c.Call(ctx, methods.MethodGasPrice)
	if err != nil {
		return nil, decision, err
	}
	value, err := c.decodedQuantity(methods.MethodGasPrice, decision)
	if err != nil {
		return nil, decision, err
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
	value, err := c.decodedQuantity(methods.MethodPeerCount, decision)
	if err != nil {
		return 0, decision, err
	}
	if !value.IsUint64() {
		return 0, decision, fmt.Errorf("ethguard: invalid %s %q", methods.MethodPeerCount, decision.Value)
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

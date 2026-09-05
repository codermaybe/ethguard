// Package ethadapter presents EthGuard's verified aggregation behind an
// ethclient-shaped surface, so code written against go-ethereum's client can
// switch with minimal friction:
//
//	client, err := ethadapter.New(cfg)   // was: ethclient.Dial(url)
//
// Compatibility is method-shaped, not interface-claimed: the adapter offers
// same-named methods with identical signatures for the subset backed by
// verified policies. Anything beyond the subset stays reachable through the
// wrapped *ethguard.Client (Unwrap) and its generic Call.
//
// Semantics worth knowing before switching:
//
//   - Moving references ("latest") resolve through the configured anchoring
//     strategy and its cache, exactly like any other anchored read; results
//     are pinned to a cross-verified reference rather than racing heads.
//   - BlockNumber reports the height of that verified reference — a consensus
//     override in the proxyd tradition, not one arbitrary provider's tip.
//   - Every failure keeps EthGuard's classified sentinels: errors.Is works
//     against the ethguard error surface unchanged.
package ethadapter

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"ethguard"
	"ethguard/config"
	"ethguard/rpc/methods"
)

// Client adapts an ethguard.Client to ethclient-shaped calls.
type Client struct {
	eg *ethguard.Client
}

// New builds the underlying verified client from configuration and returns
// it in ethclient shape. Options pass through unchanged.
func New(cfg config.Config, opts ...ethguard.Option) (*Client, error) {
	eg, err := ethguard.New(cfg, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{eg: eg}, nil
}

// Wrap adapts an already-built ethguard.Client.
func Wrap(eg *ethguard.Client) *Client {
	return &Client{eg: eg}
}

// Unwrap exposes the underlying client for methods beyond this adapter's
// verified subset (generic Call, Pool, lifecycle).
func (c *Client) Unwrap() *ethguard.Client { return c.eg }

// Close releases all upstream connections owned by the client.
func (c *Client) Close() { c.eg.Close() }

// ChainID returns the fleet-verified chain ID.
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	id, _, err := c.eg.ChainID(ctx)
	return id, err
}

// BalanceAt returns the verified balance of account at blockNumber; nil
// means the currently verified reference behind "latest".
func (c *Client) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	value, _, err := ethguard.CallInto[hexutil.Big](
		ctx, c.eg, methods.MethodGetBalance, account.Hex(), toBlockNumArg(blockNumber))
	if err != nil {
		return nil, err
	}
	return (*big.Int)(value), nil
}

// BlockByHash returns the verified block with the given hash.
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	block, _, err := c.eg.BlockByHash(ctx, hash, true)
	return block, err
}

// BlockByNumber returns the verified block at number; nil pins the moving
// "latest" tag to the currently verified reference first.
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if number == nil || number.Sign() < 0 {
		ref, err := c.eg.AnchorBlockHash(ctx)
		if err != nil {
			return nil, err
		}
		return c.blockAt(ctx, ref)
	}
	return c.blockAt(ctx, toBlockNumArg(number))
}

// BlockNumber returns the height of the currently verified block reference —
// a derivation over verified primitives, never one provider's tip. The full
// selection reasoning lives on the root Client.BlockNumber.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.eg.BlockNumber(ctx)
}

// TransactionReceipt returns the fleet-verified receipt for txHash;
// ethereum.NotFound when the fleet verifies the transaction has no receipt
// yet, matching go-ethereum's own client. The hash addresses immutable
// content, so no anchoring or freshness machinery applies.
func (c *Client) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	receipt, _, err := c.eg.TransactionReceipt(ctx, txHash)
	return receipt, err
}

// TransactionByHash returns the fleet-verified transaction for txHash and
// whether the fleet saw it as still pending; ethereum.NotFound when the
// fleet verifies no such transaction exists. Pending transactions are
// ordinary verified objects here: while they stay pending, honest sources
// hold identical payloads.
func (c *Client) TransactionByHash(ctx context.Context, txHash common.Hash) (*types.Transaction, bool, error) {
	decision, err := c.eg.Call(ctx, methods.MethodGetTransactionByHash, txHash.Hex())
	if err != nil {
		return nil, false, err
	}
	tx, pending, err := ethguard.DecodeTransaction(decision.Value)
	if err != nil {
		return nil, false, err
	}
	if tx.Hash() != txHash {
		return nil, false, fmt.Errorf("%w: transaction hash %s does not match requested hash %s",
			ethguard.ErrMalformedResponse, tx.Hash(), txHash)
	}
	return tx, pending, nil
}

// BlockReceipts returns the fleet-verified receipts of one block. Numbers
// and moving tags are pinned to the verified reference before fan-out;
// hashes address immutable content directly. Verified absence of the block
// maps to ethereum.NotFound.
func (c *Client) BlockReceipts(ctx context.Context, blockNrOrHash gethrpc.BlockNumberOrHash) ([]*types.Receipt, error) {
	ref, err := blockRefArg(blockNrOrHash)
	if err != nil {
		return nil, err
	}
	decision, err := c.eg.Call(ctx, methods.MethodGetBlockReceipts, ref)
	if err != nil {
		return nil, err
	}
	receipts, err := ethguard.DecodeReceipts(decision.Value)
	if err != nil {
		return nil, err
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		for i, receipt := range receipts {
			if receipt.BlockHash != hash {
				return nil, fmt.Errorf("%w: receipt %d block hash %s does not match requested hash %s",
					ethguard.ErrMalformedResponse, i, receipt.BlockHash, hash)
			}
		}
	}
	return receipts, nil
}

// NonceAt returns the verified account nonce of account at blockNumber;
// nil pins the moving tag to the currently verified reference.
func (c *Client) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	nonce, _, err := ethguard.CallInto[hexutil.Uint64](
		ctx, c.eg, methods.MethodGetTransactionCount, account.Hex(), toBlockNumArg(blockNumber))
	if err != nil {
		return 0, err
	}
	return uint64(*nonce), nil
}

// StorageAt returns the verified value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently verified reference.
func (c *Client) StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	value, _, err := ethguard.CallInto[hexutil.Bytes](
		ctx, c.eg, methods.MethodGetStorageAt, account.Hex(), key.Hex(), toBlockNumArg(blockNumber))
	if err != nil {
		return nil, err
	}
	return *value, nil
}

// CodeAt returns the verified contract code of account at blockNumber; nil
// pins the moving tag to the currently verified reference. An externally
// owned account answers empty code, not an error.
func (c *Client) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	code, _, err := ethguard.CallInto[hexutil.Bytes](
		ctx, c.eg, methods.MethodGetCode, account.Hex(), toBlockNumArg(blockNumber))
	if err != nil {
		return nil, err
	}
	return *code, nil
}

// SuggestGasTipCap returns the fleet's lower-median priority-fee estimate —
// a variable result resolved by robust selection, never claimed verified.
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	tip, _, err := c.eg.MaxPriorityFeePerGas(ctx)
	return tip, err
}

// blockRefArg converts go-ethereum's BlockNumberOrHash into the plain
// string reference the anchored policies rewrite. The object wire form is
// deliberately avoided: an object-shaped moving tag would slip past the
// rewrite unrewritten and turn the read into inevitable dissent.
func blockRefArg(blockNrOrHash gethrpc.BlockNumberOrHash) (string, error) {
	if hash, ok := blockNrOrHash.Hash(); ok {
		return hash.Hex(), nil
	}
	if number, ok := blockNrOrHash.Number(); ok {
		switch number {
		case gethrpc.PendingBlockNumber:
			return "pending", nil
		case gethrpc.LatestBlockNumber:
			return "latest", nil
		case gethrpc.SafeBlockNumber:
			return "safe", nil
		case gethrpc.FinalizedBlockNumber:
			return "finalized", nil
		default:
			if number < 0 {
				return "", fmt.Errorf("ethadapter: unsupported block number %d", number)
			}
			return toBlockNumArg(big.NewInt(number.Int64())), nil
		}
	}
	return "", fmt.Errorf("ethadapter: empty block number or hash")
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed verified.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	price, _, err := c.eg.GasPrice(ctx)
	return price, err
}

// PeerCount returns one healthy source's count of its own peers.
func (c *Client) PeerCount(ctx context.Context) (uint64, error) {
	count, _, err := c.eg.PeerCount(ctx)
	return count, err
}

// SyncProgress reports the consulted source's sync state; nil means it
// considers itself fully synced.
func (c *Client) SyncProgress(ctx context.Context) (*ethereum.SyncProgress, error) {
	progress, _, err := c.eg.Syncing(ctx)
	return progress, err
}

// blockAt fetches and strictly compares one block identified explicitly by
// hash or height — never by a moving tag. Which method the reference rides
// is a wire-portability decision: the first live soak run caught public
// endpoints rejecting a 32-byte hash in eth_getBlockByNumber's number
// position ("hex number > 64 bits"), so hash-looking references take the
// hash-addressed method and everything else the number-addressed one.
func (c *Client) blockAt(ctx context.Context, ref string) (*types.Block, error) {
	method := methods.MethodGetBlockByNumber
	if len(ref) == 2+2*common.HashLength && strings.HasPrefix(ref, "0x") {
		method = methods.MethodGetBlockByHash
	}
	decision, err := c.eg.Call(ctx, method, ref, true)
	if err != nil {
		return nil, err
	}
	return ethguard.DecodeBlock(decision.Value)
}

// toBlockNumArg mirrors go-ethereum's conversion of a block number argument.
func toBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	return hexutil.EncodeBig(number)
}

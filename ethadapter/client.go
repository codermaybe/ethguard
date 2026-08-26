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
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

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
	return c.blockAt(ctx, hash.Hex())
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

// BlockNumber returns the height of the currently verified block reference.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	block, err := c.BlockByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	return block.NumberU64(), nil
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed verified.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	price, _, err := c.eg.GasPrice(ctx)
	return price, err
}

// blockAt fetches and strictly compares one block identified explicitly by
// hash or height — never by a moving tag.
func (c *Client) blockAt(ctx context.Context, ref string) (*types.Block, error) {
	decision, err := c.eg.Call(ctx, methods.MethodGetBlockByNumber, ref, true)
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

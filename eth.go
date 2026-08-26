package ethguard

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
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

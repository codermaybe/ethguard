package ethguard

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strconv"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func (c *Client) pendingBalanceAtExec(ctx context.Context, account common.Address) (*big.Int, CallResult, error) {
	return readQuantityExec(ctx, c, methods.MethodGetBalance, account.Hex(), "pending")
}
func (c *Client) PendingBalanceAt(ctx context.Context, account common.Address) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.pendingBalanceAtExec(opCtx, account)
	})
}

func (c *Client) pendingStorageAtExec(ctx context.Context, account common.Address, key common.Hash) ([]byte, CallResult, error) {
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, account.Hex(), key.Hex(), "pending")
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return []byte(*value), CallResult{Decision: decisionOf(decision)}, nil
}
func (c *Client) PendingStorageAt(ctx context.Context, account common.Address, key common.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.pendingStorageAtExec(opCtx, account, key)
	})
}

func (c *Client) pendingCodeAtExec(ctx context.Context, account common.Address) ([]byte, CallResult, error) {
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, account.Hex(), "pending")
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return []byte(*value), CallResult{Decision: decisionOf(decision)}, nil
}
func (c *Client) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) { return c.pendingCodeAtExec(opCtx, account) })
}

func (c *Client) pendingNonceAtExec(ctx context.Context, account common.Address) (uint64, CallResult, error) {
	value, decision, err := aggregateInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, account.Hex(), "pending")
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint64(*value), CallResult{Decision: decisionOf(decision)}, nil
}
func (c *Client) PendingNonceAt(ctx context.Context, account common.Address) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) { return c.pendingNonceAtExec(opCtx, account) })
}

func (c *Client) transactionCountExec(ctx context.Context, blockHash common.Hash) (uint, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodGetBlockTransactionCountByHash, blockHash.Hex())
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	if bytes.Equal(bytes.TrimSpace(decision.Value), []byte("null")) {
		return 0, CallResult{Decision: decisionOf(decision)}, ethereum.NotFound
	}
	canonical, err := c.Normalize(methods.MethodGetBlockTransactionCountByHash, decision.Value)
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: decode %s: %v", rpc.ErrMalformedResponse, methods.MethodGetBlockTransactionCountByHash, err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok || !value.IsUint64() || (strconv.IntSize == 32 && value.BitLen() > 32) {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: transaction count out of range %q", rpc.ErrMalformedResponse, canonical)
	}
	return uint(value.Uint64()), CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionCount returns the fleet-corroborated transaction count for an
// immutable block hash. A corroborated JSON null maps to ethereum.NotFound.
func (c *Client) TransactionCount(ctx context.Context, blockHash common.Hash) (uint, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint, CallResult, error) {
		return c.transactionCountExec(opCtx, blockHash)
	})
}

func (c *Client) transactionInBlockExec(ctx context.Context, blockHash common.Hash, index uint) (*types.Transaction, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByBlockHashAndIndex, blockHash.Hex(), hexutil.Uint64(index))
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	decoded, err := typed.DecodeTransactionInBlock(decision.Value, blockHash, uint64(index))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return decoded.Tx, CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionInBlock returns the transaction at an index in an immutable
// block, or ethereum.NotFound when the block/index has no transaction.
func (c *Client) TransactionInBlock(ctx context.Context, blockHash common.Hash, index uint) (*types.Transaction, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*types.Transaction, CallResult, error) {
		return c.transactionInBlockExec(opCtx, blockHash, index)
	})
}

func (c *Client) callContractAtHashExec(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) ([]byte, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodCall, toCallArg(msg), hashBlockArg(blockHash))
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}
func (c *Client) CallContractAtHash(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.callContractAtHashExec(opCtx, msg, blockHash)
	})
}

func (c *Client) pendingCallContractExec(ctx context.Context, msg ethereum.CallMsg) ([]byte, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodCall, toCallArg(msg), "pending")
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}
func (c *Client) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) { return c.pendingCallContractExec(opCtx, msg) })
}

func (c *Client) estimateGasWithArgExec(ctx context.Context, call map[string]any, blockArg any) (uint64, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodEstimateGas, call, blockArg)
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	gas, err := typed.DecodeGasEstimate(decision.Value)
	return gas, CallResult{Decision: decisionOf(decision)}, err
}
func (c *Client) estimateGasWithArg(ctx context.Context, call map[string]any, blockArg any) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.estimateGasWithArgExec(opCtx, call, blockArg)
	})
}
func (c *Client) EstimateGasAtBlock(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		arg, err := checkedBlockNumArg(blockNumber)
		if err != nil {
			return 0, CallResult{}, err
		}
		return c.estimateGasWithArgExec(opCtx, toCallArg(msg), arg)
	})
}
func (c *Client) EstimateGasAtBlockHash(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) (uint64, CallResult, error) {
	return c.estimateGasWithArg(ctx, toCallArg(msg), hashBlockArg(blockHash))
}

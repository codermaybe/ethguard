package ethguard

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strconv"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func (c *Client) pendingBalanceAtExec(ctx context.Context, account ethapi.Address) (*big.Int, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	return readQuantityExec(ctx, c, methods.MethodGetBalance, string(account), "pending")
}
func (c *Client) PendingBalanceAt(ctx context.Context, account ethapi.Address) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.pendingBalanceAtExec(opCtx, account)
	})
}

func (c *Client) pendingStorageAtExec(ctx context.Context, account ethapi.Address, key ethapi.Hash) ([]byte, CallResult, error) {
	account, key, err := validateAddressHash(account, key)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, string(account), string(key), "pending")
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return []byte(*value), CallResult{Decision: decisionOf(decision)}, nil
}
func (c *Client) PendingStorageAt(ctx context.Context, account ethapi.Address, key ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.pendingStorageAtExec(opCtx, account, key)
	})
}

func (c *Client) pendingCodeAtExec(ctx context.Context, account ethapi.Address) ([]byte, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, string(account), "pending")
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return []byte(*value), CallResult{Decision: decisionOf(decision)}, nil
}
func (c *Client) PendingCodeAt(ctx context.Context, account ethapi.Address) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) { return c.pendingCodeAtExec(opCtx, account) })
}

func (c *Client) pendingNonceAtExec(ctx context.Context, account ethapi.Address) (uint64, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return 0, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, string(account), "pending")
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint64(*value), CallResult{Decision: decisionOf(decision)}, nil
}
func (c *Client) PendingNonceAt(ctx context.Context, account ethapi.Address) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) { return c.pendingNonceAtExec(opCtx, account) })
}

func (c *Client) transactionCountExec(ctx context.Context, blockHash ethapi.Hash) (uint, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return 0, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetBlockTransactionCountByHash, string(blockHash))
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
func (c *Client) TransactionCount(ctx context.Context, blockHash ethapi.Hash) (uint, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint, CallResult, error) {
		return c.transactionCountExec(opCtx, blockHash)
	})
}

func (c *Client) transactionInBlockExec(ctx context.Context, blockHash ethapi.Hash, index uint) (*ethapi.Transaction, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByBlockHashAndIndex, string(blockHash), hexutil.Uint64(index))
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
func (c *Client) TransactionInBlock(ctx context.Context, blockHash ethapi.Hash, index uint) (*ethapi.Transaction, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*ethapi.Transaction, CallResult, error) {
		return c.transactionInBlockExec(opCtx, blockHash, index)
	})
}

func (c *Client) transactionSenderExec(ctx context.Context, tx *ethapi.Transaction, blockHash ethapi.Hash, index uint) (ethapi.Address, CallResult, error) {
	// The caller's value is re-validated and its signed-payload hash
	// recomputed before any network contact: a nil, zero-selected,
	// multi-selected, structurally illegal, unsigned, or misdeclared input
	// fails locally instead of binding a foreign sender.
	payload, err := typed.TransactionPayload(tx)
	if err != nil {
		return "", CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return "", CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodGetTransactionByBlockHashAndIndex, string(blockHash), hexutil.Uint64(index))
	if err != nil {
		decision.Value = nil
		return "", CallResult{Decision: decisionOf(decision)}, err
	}
	decoded, err := typed.DecodeTransactionInBlock(decision.Value, blockHash, uint64(index))
	if err != nil {
		return "", CallResult{Decision: decisionOf(decision)}, err
	}
	if string(decoded.TxHash) != payload.Hash().Hex() {
		return "", CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: remote transaction hash %s does not match input %s", rpc.ErrMalformedResponse, decoded.TxHash, payload.Hash())
	}
	if !decoded.HasFrom {
		return "", CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: transaction response has no sender", rpc.ErrMalformedResponse)
	}
	return decoded.From, CallResult{Decision: decisionOf(decision)}, nil
}

// TransactionSender returns the sender reported for the transaction at the
// requested immutable block/index. It deliberately performs a fresh RPC read
// and never relies on hidden sender state attached to the transaction value;
// the returned address is the RPC-declared sender at this decision's trust
// level, not a recovered or inclusion-proven identity.
func (c *Client) TransactionSender(ctx context.Context, tx *ethapi.Transaction, blockHash ethapi.Hash, index uint) (ethapi.Address, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (ethapi.Address, CallResult, error) {
		return c.transactionSenderExec(opCtx, tx, blockHash, index)
	})
}

func (c *Client) callContractAtHashExec(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	blockHash, err := validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	call, err := toCallArg(msg)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, call, hashBlockArg(blockHash))
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}
func (c *Client) CallContractAtHash(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.callContractAtHashExec(opCtx, msg, blockHash)
	})
}

func (c *Client) pendingCallContractExec(ctx context.Context, msg ethapi.CallMsg) ([]byte, CallResult, error) {
	call, err := toCallArg(msg)
	if err != nil {
		return nil, CallResult{}, err
	}
	decision, err := c.callTyped(ctx, methods.MethodCall, call, "pending")
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	data, err := decodeCallDecision(decision)
	return data, CallResult{Decision: decisionOf(decision)}, err
}
func (c *Client) PendingCallContract(ctx context.Context, msg ethapi.CallMsg) ([]byte, CallResult, error) {
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
func (c *Client) EstimateGasAtBlock(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		arg, err := checkedBlockNumArg(blockNumber)
		if err != nil {
			return 0, CallResult{}, err
		}
		call, err := toCallArg(msg)
		if err != nil {
			return 0, CallResult{}, err
		}
		return c.estimateGasWithArgExec(opCtx, call, arg)
	})
}
func (c *Client) EstimateGasAtBlockHash(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		blockHash, err := validateHash(blockHash)
		if err != nil {
			return 0, CallResult{}, err
		}
		call, err := toCallArg(msg)
		if err != nil {
			return 0, CallResult{}, err
		}
		return c.estimateGasWithArgExec(opCtx, call, hashBlockArg(blockHash))
	})
}

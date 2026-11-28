package ethguard

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// ChainID returns the fleet-corroborated chain ID.
func (c *Client) ChainID(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodChainID)
}

// NetworkID returns the corroborated legacy chain identity (net_version
// spells in decimal what eth_chainId spells in hex).
func (c *Client) NetworkID(ctx context.Context) (*big.Int, CallResult, error) {
	return readQuantity(ctx, c, false, methods.MethodNetVersion)
}

// BalanceAt returns the corroborated balance of account at blockNumber; nil
// means the currently corroborated reference behind "latest".
func (c *Client) BalanceAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.balanceAtExec(opCtx, account, blockNumber)
	})
}

func (c *Client) balanceAtExec(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (*big.Int, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Big](ctx, c, methods.MethodGetBalance, string(account), blockArg)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return (*big.Int)(value), CallResult{Decision: decisionOf(decision)}, nil
}

// BalanceAtHash returns the corroborated balance of account in the state at
// blockHash. The hash pin is sent as EIP-1898 with requireCanonical=false,
// matching go-ethereum's BalanceAtHash contract.
func (c *Client) BalanceAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.balanceAtHashExec(opCtx, account, blockHash)
	})
}

func (c *Client) balanceAtHashExec(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (*big.Int, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Big](ctx, c, methods.MethodGetBalance, string(account), hashBlockArg(blockHash))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return (*big.Int)(value), CallResult{Decision: decisionOf(decision)}, nil
}

func (c *Client) PendingBalanceAt(ctx context.Context, account ethapi.Address) (*big.Int, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return c.pendingBalanceAtExec(opCtx, account)
	})
}

func (c *Client) pendingBalanceAtExec(ctx context.Context, account ethapi.Address) (*big.Int, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	return readQuantityExec(ctx, c, methods.MethodGetBalance, string(account), "pending")
}

// NonceAt returns the corroborated account nonce of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference.
func (c *Client) NonceAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.nonceAtExec(opCtx, account, blockNumber)
	})
}

func (c *Client) nonceAtExec(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (uint64, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return 0, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return 0, CallResult{}, err
	}
	nonce, decision, err := aggregateInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, string(account), blockArg)
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint64(*nonce), CallResult{Decision: decisionOf(decision)}, nil
}

// NonceAtHash returns the corroborated account nonce in the state at
// blockHash. The hash pin is sent as EIP-1898 with requireCanonical=false.
func (c *Client) NonceAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) {
		return c.nonceAtHashExec(opCtx, account, blockHash)
	})
}

func (c *Client) nonceAtHashExec(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (uint64, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return 0, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return 0, CallResult{}, err
	}
	nonce, decision, err := aggregateInto[hexutil.Uint64](ctx, c, methods.MethodGetTransactionCount, string(account), hashBlockArg(blockHash))
	if err != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	return uint64(*nonce), CallResult{Decision: decisionOf(decision)}, nil
}

func (c *Client) PendingNonceAt(ctx context.Context, account ethapi.Address) (uint64, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) (uint64, CallResult, error) { return c.pendingNonceAtExec(opCtx, account) })
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

// StorageAt returns the corroborated value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
func (c *Client) StorageAt(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockNumber *big.Int) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.storageAtExec(opCtx, account, key, blockNumber)
	})
}

func (c *Client) storageAtExec(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockNumber *big.Int) ([]byte, CallResult, error) {
	account, key, err := validateAddressHash(account, key)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, string(account), string(key), blockArg)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *value, CallResult{Decision: decisionOf(decision)}, nil
}

// StorageAtHash returns the corroborated value of key in account's storage
// at blockHash. The hash pin is sent as EIP-1898 with
// requireCanonical=false.
func (c *Client) StorageAtHash(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.storageAtHashExec(opCtx, account, key, blockHash)
	})
}

func (c *Client) storageAtHashExec(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	account, key, err := validateAddressHash(account, key)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	value, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetStorageAt, string(account), string(key), hashBlockArg(blockHash))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *value, CallResult{Decision: decisionOf(decision)}, nil
}

func (c *Client) PendingStorageAt(ctx context.Context, account ethapi.Address, key ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.pendingStorageAtExec(opCtx, account, key)
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

// CodeAt returns the corroborated contract code of account at blockNumber; nil
// pins the moving tag to the currently corroborated reference. An externally
// owned account answers empty code, not an error.
func (c *Client) CodeAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.codeAtExec(opCtx, account, blockNumber)
	})
}

func (c *Client) codeAtExec(ctx context.Context, account ethapi.Address, blockNumber *big.Int) ([]byte, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockArg, err := checkedBlockNumArg(blockNumber)
	if err != nil {
		return nil, CallResult{}, err
	}
	code, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, string(account), blockArg)
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *code, CallResult{Decision: decisionOf(decision)}, nil
}

// CodeAtHash returns the corroborated contract code of account at blockHash.
// The hash pin is sent as EIP-1898 with requireCanonical=false.
func (c *Client) CodeAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) {
		return c.codeAtHashExec(opCtx, account, blockHash)
	})
}

func (c *Client) codeAtHashExec(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) ([]byte, CallResult, error) {
	account, err := validateAddress(account)
	if err != nil {
		return nil, CallResult{}, err
	}
	blockHash, err = validateHash(blockHash)
	if err != nil {
		return nil, CallResult{}, err
	}
	code, decision, err := aggregateInto[hexutil.Bytes](ctx, c, methods.MethodGetCode, string(account), hashBlockArg(blockHash))
	if err != nil {
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	return *code, CallResult{Decision: decisionOf(decision)}, nil
}

func (c *Client) PendingCodeAt(ctx context.Context, account ethapi.Address) ([]byte, CallResult, error) {
	return operation(c, ctx, false, func(opCtx context.Context) ([]byte, CallResult, error) { return c.pendingCodeAtExec(opCtx, account) })
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

// PeerCount returns one healthy source's count of its own peers — an
// upstream-local result, selected not compared.
func (c *Client) PeerCount(ctx context.Context) (uint64, CallResult, error) {
	return operation(c, ctx, false, c.peerCountExec)
}

func (c *Client) peerCountExec(ctx context.Context) (uint64, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodPeerCount)
	if err != nil {
		decision.Value = nil
		return 0, CallResult{Decision: decisionOf(decision)}, err
	}
	value, projectErr := c.decodedQuantity(methods.MethodPeerCount, decision)
	if projectErr != nil {
		return 0, CallResult{Decision: decisionOf(decision)}, projectErr
	}
	if !value.IsUint64() {
		return 0, CallResult{Decision: decisionOf(decision)}, fmt.Errorf("%w: invalid %s %q", rpc.ErrMalformedResponse, methods.MethodPeerCount, decision.Value)
	}
	return value.Uint64(), CallResult{Decision: decisionOf(decision)}, nil
}

// SyncProgress reports the consulted source's sync state; nil means it
// considers itself fully synced.
func (c *Client) SyncProgress(ctx context.Context) (*ethapi.SyncProgress, CallResult, error) {
	return operation(c, ctx, false, c.syncProgressExec)
}

func (c *Client) syncProgressExec(ctx context.Context) (*ethapi.SyncProgress, CallResult, error) {
	decision, err := c.callTyped(ctx, methods.MethodSyncing)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	progress, projectErr := typed.DecodeSyncProgress(decision.Value)
	return progress, CallResult{Decision: decisionOf(decision)}, projectErr
}

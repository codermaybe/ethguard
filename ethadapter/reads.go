package ethadapter

import (
	"context"
	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
)

// ChainID executes through the shared core and preserves its result capture.
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	return execute(c, ctx, "ChainID", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) { return c.core.ChainID(ctx) }, identity[*big.Int])
}

// NetworkID executes through the shared core and preserves its result capture.
func (c *Client) NetworkID(ctx context.Context) (*big.Int, error) {
	return execute(c, ctx, "NetworkID", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) { return c.core.NetworkID(ctx) }, identity[*big.Int])
}

// BlockNumber executes through the shared core and preserves its result capture.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return execute(c, ctx, "BlockNumber", ConversionReport{}, nil, func() (uint64, ethguard.CallResult, error) { return c.core.BlockNumber(ctx) }, identity[uint64])
}

// PeerCount executes through the shared core and preserves its result capture.
func (c *Client) PeerCount(ctx context.Context) (uint64, error) {
	return execute(c, ctx, "PeerCount", ConversionReport{}, nil, func() (uint64, ethguard.CallResult, error) { return c.core.PeerCount(ctx) }, identity[uint64])
}

// PendingTransactionCount executes through the shared core and preserves its result capture.
func (c *Client) PendingTransactionCount(ctx context.Context) (uint, error) {
	return execute(c, ctx, "PendingTransactionCount", ConversionReport{}, nil, func() (uint, ethguard.CallResult, error) { return c.core.PendingTransactionCount(ctx) }, identity[uint])
}

// SuggestGasPrice executes through the shared core and preserves its result capture.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return execute(c, ctx, "SuggestGasPrice", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) { return c.core.SuggestGasPrice(ctx) }, identity[*big.Int])
}

// SuggestGasTipCap executes through the shared core and preserves its result capture.
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return execute(c, ctx, "SuggestGasTipCap", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) { return c.core.SuggestGasTipCap(ctx) }, identity[*big.Int])
}

// BlobBaseFee executes through the shared core and preserves its result capture.
func (c *Client) BlobBaseFee(ctx context.Context) (*big.Int, error) {
	return execute(c, ctx, "BlobBaseFee", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) { return c.core.BlobBaseFee(ctx) }, identity[*big.Int])
}

// BalanceAt executes through the shared core and preserves its result capture.
func (c *Client) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return execute(c, ctx, "BalanceAt", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) {
		return c.core.BalanceAt(ctx, address(account), blockNumber)
	}, identity[*big.Int])
}

// BalanceAtHash executes through the shared core and preserves its result capture.
func (c *Client) BalanceAtHash(ctx context.Context, account common.Address, blockHash common.Hash) (*big.Int, error) {
	return execute(c, ctx, "BalanceAtHash", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) {
		return c.core.BalanceAtHash(ctx, address(account), hash(blockHash))
	}, identity[*big.Int])
}

// PendingBalanceAt executes through the shared core and preserves its result capture.
func (c *Client) PendingBalanceAt(ctx context.Context, account common.Address) (*big.Int, error) {
	return execute(c, ctx, "PendingBalanceAt", ConversionReport{}, nil, func() (*big.Int, ethguard.CallResult, error) { return c.core.PendingBalanceAt(ctx, address(account)) }, identity[*big.Int])
}

// NonceAt executes through the shared core and preserves its result capture.
func (c *Client) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	return execute(c, ctx, "NonceAt", ConversionReport{}, nil, func() (uint64, ethguard.CallResult, error) { return c.core.NonceAt(ctx, address(account), blockNumber) }, identity[uint64])
}

// NonceAtHash executes through the shared core and preserves its result capture.
func (c *Client) NonceAtHash(ctx context.Context, account common.Address, blockHash common.Hash) (uint64, error) {
	return execute(c, ctx, "NonceAtHash", ConversionReport{}, nil, func() (uint64, ethguard.CallResult, error) {
		return c.core.NonceAtHash(ctx, address(account), hash(blockHash))
	}, identity[uint64])
}

// PendingNonceAt executes through the shared core and preserves its result capture.
func (c *Client) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return execute(c, ctx, "PendingNonceAt", ConversionReport{}, nil, func() (uint64, ethguard.CallResult, error) { return c.core.PendingNonceAt(ctx, address(account)) }, identity[uint64])
}

// StorageAt executes through the shared core and preserves its result capture.
func (c *Client) StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	return execute(c, ctx, "StorageAt", ConversionReport{}, nil, func() ([]byte, ethguard.CallResult, error) {
		return c.core.StorageAt(ctx, address(account), hash(key), blockNumber)
	}, identity[[]byte])
}

// StorageAtHash executes through the shared core and preserves its result capture.
func (c *Client) StorageAtHash(ctx context.Context, account common.Address, key common.Hash, blockHash common.Hash) ([]byte, error) {
	return execute(c, ctx, "StorageAtHash", ConversionReport{}, nil, func() ([]byte, ethguard.CallResult, error) {
		return c.core.StorageAtHash(ctx, address(account), hash(key), hash(blockHash))
	}, identity[[]byte])
}

// PendingStorageAt executes through the shared core and preserves its result capture.
func (c *Client) PendingStorageAt(ctx context.Context, account common.Address, key common.Hash) ([]byte, error) {
	return execute(c, ctx, "PendingStorageAt", ConversionReport{}, nil, func() ([]byte, ethguard.CallResult, error) {
		return c.core.PendingStorageAt(ctx, address(account), hash(key))
	}, identity[[]byte])
}

// CodeAt executes through the shared core and preserves its result capture.
func (c *Client) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	return execute(c, ctx, "CodeAt", ConversionReport{}, nil, func() ([]byte, ethguard.CallResult, error) { return c.core.CodeAt(ctx, address(account), blockNumber) }, identity[[]byte])
}

// CodeAtHash executes through the shared core and preserves its result capture.
func (c *Client) CodeAtHash(ctx context.Context, account common.Address, blockHash common.Hash) ([]byte, error) {
	return execute(c, ctx, "CodeAtHash", ConversionReport{}, nil, func() ([]byte, ethguard.CallResult, error) {
		return c.core.CodeAtHash(ctx, address(account), hash(blockHash))
	}, identity[[]byte])
}

// PendingCodeAt executes through the shared core and preserves its result capture.
func (c *Client) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	return execute(c, ctx, "PendingCodeAt", ConversionReport{}, nil, func() ([]byte, ethguard.CallResult, error) { return c.core.PendingCodeAt(ctx, address(account)) }, identity[[]byte])
}

// TransactionCount executes through the shared core and preserves its result capture.
func (c *Client) TransactionCount(ctx context.Context, blockHash common.Hash) (uint, error) {
	return execute(c, ctx, "TransactionCount", ConversionReport{}, nil, func() (uint, ethguard.CallResult, error) { return c.core.TransactionCount(ctx, hash(blockHash)) }, identity[uint])
}

// HeaderByHash executes through core and validates the target representation.
func (c *Client) HeaderByHash(ctx context.Context, h common.Hash) (*types.Header, error) {
	return execute(c, ctx, "HeaderByHash", ConversionReport{}, nil, func() (*ethapi.Header, ethguard.CallResult, error) { return c.core.HeaderByHash(ctx, hash(h)) }, headerResult)
}

// HeaderByNumber executes through core and validates the target representation.
func (c *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return execute(c, ctx, "HeaderByNumber", ConversionReport{}, nil, func() (*ethapi.Header, ethguard.CallResult, error) { return c.core.HeaderByNumber(ctx, number) }, headerResult)
}

// BlockByHash executes through core and validates the target representation.
func (c *Client) BlockByHash(ctx context.Context, h common.Hash) (*types.Block, error) {
	return execute(c, ctx, "BlockByHash", ConversionReport{}, nil, func() (*ethapi.Block, ethguard.CallResult, error) { return c.core.BlockByHash(ctx, hash(h)) }, blockResult)
}

// BlockByNumber executes through core and validates the target representation.
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	return execute(c, ctx, "BlockByNumber", ConversionReport{}, nil, func() (*ethapi.Block, ethguard.CallResult, error) { return c.core.BlockByNumber(ctx, number) }, blockResult)
}

// TransactionInBlock executes through core and validates the target representation.
func (c *Client) TransactionInBlock(ctx context.Context, blockHash common.Hash, index uint) (*types.Transaction, error) {
	return execute(c, ctx, "TransactionInBlock", ConversionReport{}, nil, func() (*ethapi.Transaction, ethguard.CallResult, error) {
		return c.core.TransactionInBlock(ctx, hash(blockHash), index)
	}, transactionResult)
}

// TransactionReceipt executes through core and validates the target representation.
func (c *Client) TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	return execute(c, ctx, "TransactionReceipt", ConversionReport{}, nil, func() (*ethapi.Receipt, ethguard.CallResult, error) { return c.core.TransactionReceipt(ctx, hash(h)) }, receiptResult)
}

func headerResult(v *ethapi.Header, _ ethguard.CallResult) (*types.Header, ConversionReport, error) {
	return ToGethHeader(v)
}
func transactionResult(v *ethapi.Transaction, _ ethguard.CallResult) (*types.Transaction, ConversionReport, error) {
	return ToGethTransaction(v)
}
func receiptResult(v *ethapi.Receipt, _ ethguard.CallResult) (*types.Receipt, ConversionReport, error) {
	return ToGethReceipt(v)
}

type nativeTransaction struct {
	tx      *ethapi.Transaction
	pending bool
}
type gethTransaction struct {
	tx      *types.Transaction
	pending bool
}

// TransactionByHash preserves core's pending determination and validates the transaction.
func (c *Client) TransactionByHash(ctx context.Context, h common.Hash) (*types.Transaction, bool, error) {
	v, err := execute(c, ctx, "TransactionByHash", ConversionReport{}, nil, func() (nativeTransaction, ethguard.CallResult, error) {
		tx, pending, result, err := c.core.TransactionByHash(ctx, hash(h))
		return nativeTransaction{tx, pending}, result, err
	}, func(v nativeTransaction, _ ethguard.CallResult) (gethTransaction, ConversionReport, error) {
		tx, report, err := ToGethTransaction(v.tx)
		return gethTransaction{tx, v.pending}, report, err
	})
	return v.tx, v.pending, err
}

// TransactionSender uses core's fresh immutable block/index lookup; it does not
// return a cached or locally recovered sender.
func (c *Client) TransactionSender(ctx context.Context, tx *types.Transaction, blockHash common.Hash, index uint) (common.Address, error) {
	input, report, err := fromTransaction(tx)
	return execute(c, ctx, "TransactionSender", report, err, func() (ethapi.Address, ethguard.CallResult, error) {
		return c.core.TransactionSender(ctx, input, hash(blockHash), index)
	}, func(a ethapi.Address, _ ethguard.CallResult) (common.Address, ConversionReport, error) {
		check := converter{kind: "TransactionSender"}
		out := check.address("from", string(a))
		return out, check.report, check.err
	})
}

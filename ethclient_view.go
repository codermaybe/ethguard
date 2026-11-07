package ethguard

import (
	"context"
	"fmt"
	"math/big"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// EthClient is the ethclient-shaped view of one Client: the official
// go-ethereum read signatures, SendTransaction, and Close — nothing else.
// It holds a private owner; it is never an embedded facade, and it adds no
// anchor, Call, Pool, Mode, Normalize, raw-write, or CreateAccessList
// surface. The view performs no networking of its own and builds no second
// pool: every method is a thin projection of the same shared execution path
// the native methods run, with the view flag telling the entry to omit raw
// material collection when no ResultCapture is bound (nothing would consume
// it). No pointer-identity or zero-allocation promise is made. It is not
// go-ethereum's concrete *ethclient.Client — the interface commitments are
// exactly bind/v2's ContractCaller and DeployBackend, checked by the API
// contract test.
type EthClient struct {
	// owner is the Client whose shared execution this view projects.
	owner *Client
}

// EthClient returns the ethclient-shaped view of this client. It does not
// dial, build a pool, or copy configuration; concurrent use follows the
// owner's contract, and Close on either surface stops both.
func (c *Client) EthClient() *EthClient {
	return &EthClient{owner: c}
}

// Close releases the owner's upstream connections — the same lifecycle as
// Client.Close, shared between both surfaces.
func (ec *EthClient) Close() { ec.owner.Close() }

// ChainID returns the fleet-corroborated chain ID.
func (ec *EthClient) ChainID(ctx context.Context) (*big.Int, error) {
	value, _, err := readQuantity(ctx, ec.owner, true, methods.MethodChainID)
	return value, err
}

// NetworkID returns the corroborated legacy chain identity (net_version
// spells in decimal what eth_chainId spells in hex).
func (ec *EthClient) NetworkID(ctx context.Context) (*big.Int, error) {
	value, _, err := readQuantity(ctx, ec.owner, true, methods.MethodNetVersion)
	return value, err
}

// BalanceAt returns the corroborated balance of account at blockNumber; nil
// means the currently corroborated reference behind "latest".
func (ec *EthClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return ec.owner.balanceAtExec(opCtx, account, blockNumber)
	})
	return value, err
}

// BalanceAtHash returns the corroborated balance of account at blockHash.
func (ec *EthClient) BalanceAtHash(ctx context.Context, account common.Address, blockHash common.Hash) (*big.Int, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return ec.owner.balanceAtHashExec(opCtx, account, blockHash)
	})
	return value, err
}

// NonceAt returns the corroborated account nonce of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference.
func (ec *EthClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.nonceAtExec(opCtx, account, blockNumber)
	})
	return value, err
}

// NonceAtHash returns the corroborated account nonce at blockHash.
func (ec *EthClient) NonceAtHash(ctx context.Context, account common.Address, blockHash common.Hash) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.nonceAtHashExec(opCtx, account, blockHash)
	})
	return value, err
}

// StorageAt returns the corroborated value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
func (ec *EthClient) StorageAt(ctx context.Context, account common.Address, key common.Hash, blockNumber *big.Int) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.storageAtExec(opCtx, account, key, blockNumber)
	})
	return value, err
}

// StorageAtHash returns the corroborated storage value at blockHash.
func (ec *EthClient) StorageAtHash(ctx context.Context, account common.Address, key common.Hash, blockHash common.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.storageAtHashExec(opCtx, account, key, blockHash)
	})
	return value, err
}

// CodeAt returns the corroborated contract code of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference. An
// externally owned account answers empty code, not an error.
func (ec *EthClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.codeAtExec(opCtx, account, blockNumber)
	})
	return value, err
}

// CodeAtHash returns the corroborated contract code at blockHash.
func (ec *EthClient) CodeAtHash(ctx context.Context, account common.Address, blockHash common.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.codeAtHashExec(opCtx, account, blockHash)
	})
	return value, err
}

// BlockByHash returns the corroborated block with the given hash.
func (ec *EthClient) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	block, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Block, CallResult, error) {
		b, decision, blockErr := ec.owner.blockAt(opCtx, hash.Hex())
		return b, CallResult{Decision: decisionOf(decision)}, blockErr
	})
	return block, err
}

// HeaderByHash returns the corroborated header with the given hash.
func (ec *EthClient) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	header, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Header, CallResult, error) {
		h, decision, headerErr := ec.owner.headerAt(opCtx, hash.Hex())
		return h, CallResult{Decision: decisionOf(decision)}, headerErr
	})
	return header, err
}

// BlockByNumber returns the corroborated block at number, with go-ethereum's
// negative-tag contract: -1 pending is rejected locally, -2/-3/-4 establish
// their own level's reference, -5 is earliest (height 0), nil pins the
// currently corroborated reference.
func (ec *EthClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	block, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Block, CallResult, error) {
		return ec.owner.blockByNumberExec(opCtx, number)
	})
	return block, err
}

// HeaderByNumber returns a corroborated header at number. The moving-tag and
// pending rules match the root Client's HeaderByNumber contract.
func (ec *EthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	header, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Header, CallResult, error) {
		return ec.owner.headerByNumberExec(opCtx, number)
	})
	return header, err
}

// BlockNumber returns the height of the currently corroborated block
// reference — a derivation over corroborated primitives, not an independent
// moving-value fan-out.
func (ec *EthClient) BlockNumber(ctx context.Context) (uint64, error) {
	height, _, err := operation(ec.owner, ctx, true, ec.owner.blockNumberExec)
	return height, err
}

// BlockReceipts returns the fleet-corroborated receipts of one block, with
// request-answer binding per reference form and corroborated absence mapped
// to ethereum.NotFound.
func (ec *EthClient) BlockReceipts(ctx context.Context, blockNrOrHash gethrpc.BlockNumberOrHash) ([]*types.Receipt, error) {
	receipts, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]*types.Receipt, CallResult, error) {
		return ec.owner.blockReceiptsExec(opCtx, blockNrOrHash)
	})
	return receipts, err
}

// TransactionReceipt returns the fleet-corroborated receipt for the
// transaction hash; ethereum.NotFound when the fleet corroborates the
// transaction has no receipt yet.
func (ec *EthClient) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	receipt, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Receipt, CallResult, error) {
		return ec.owner.transactionReceiptExec(opCtx, hash)
	})
	return receipt, err
}

// TransactionByHash returns the fleet-corroborated transaction for the hash
// and whether the fleet saw it as still pending; ethereum.NotFound when the
// fleet corroborates no such transaction exists.
func (ec *EthClient) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	tx, pending, _, err := operationPair(ec.owner, ctx, true, func(opCtx context.Context) (*types.Transaction, bool, CallResult, error) {
		return ec.owner.transactionByHashExec(opCtx, hash)
	})
	return tx, pending, err
}

// TransactionCount returns the fleet-corroborated transaction count for an
// immutable block hash. A corroborated JSON null maps to ethereum.NotFound.
func (ec *EthClient) TransactionCount(ctx context.Context, blockHash common.Hash) (uint, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint, CallResult, error) {
		return ec.owner.transactionCountExec(opCtx, blockHash)
	})
	return value, err
}

// SuggestGasTipCap returns the fleet's lower-median priority-fee estimate —
// a variable result resolved by robust selection, never claimed corroborated.
func (ec *EthClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	value, _, err := readQuantity(ctx, ec.owner, true, methods.MethodMaxPriorityFeePerGas)
	return value, err
}

// SuggestGasPrice returns the fleet's lower-median gas price — a variable
// result resolved by robust selection, never claimed corroborated.
func (ec *EthClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	value, _, err := readQuantity(ctx, ec.owner, true, methods.MethodGasPrice)
	return value, err
}

// CallContract returns the fleet-corroborated result of executing msg at
// blockNumber; nil pins the moving tag to the currently corroborated
// reference. A fleet-corroborated revert is returned as a *RevertError.
func (ec *EthClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	data, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.callContractExec(opCtx, msg, blockNumber)
	})
	return data, err
}

// EstimateGas returns the fleet's lower-median gas estimate for msg at the
// currently corroborated reference — a variable result, Selected at best.
func (ec *EthClient) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	gas, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.estimateGasExec(opCtx, msg)
	})
	return gas, err
}

// FilterLogs returns the fleet-corroborated logs matching q. Range filters
// are pinned to the corroborated reference like any other anchored read.
func (ec *EthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	logs, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]types.Log, CallResult, error) {
		return ec.owner.filterLogsExec(opCtx, q)
	})
	return logs, err
}

// PeerCount returns one healthy source's count of its own peers — an
// upstream-local result, selected not compared.
func (ec *EthClient) PeerCount(ctx context.Context) (uint64, error) {
	count, _, err := operation(ec.owner, ctx, true, ec.owner.peerCountExec)
	return count, err
}

// SyncProgress reports the consulted source's sync state; nil means it
// considers itself fully synced.
func (ec *EthClient) SyncProgress(ctx context.Context) (*ethereum.SyncProgress, error) {
	progress, _, err := operation(ec.owner, ctx, true, ec.owner.syncProgressExec)
	return progress, err
}

// FeeHistory returns the fleet-corroborated fee history ending at the
// corroborated reference: lastBlock nil pins the moving "latest" tag.
func (ec *EthClient) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethereum.FeeHistory, error) {
	history, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethereum.FeeHistory, CallResult, error) {
		return ec.owner.feeHistoryExec(opCtx, blockCount, lastBlock, rewardPercentiles)
	})
	return history, err
}

// BlobBaseFee returns the fleet's lower-median blob base fee (EIP-4844) — a
// variable result, so the decision is Selected at best.
func (ec *EthClient) BlobBaseFee(ctx context.Context) (*big.Int, error) {
	value, _, err := readQuantity(ctx, ec.owner, true, methods.MethodBlobBaseFee)
	return value, err
}

// SendTransaction mirrors go-ethereum's ethclient.SendTransaction: encode
// the signed transaction and broadcast its raw bytes. The completion result
// still publishes to a bound ResultCapture — the error is the view's only
// direct return.
func (ec *EthClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	_, err := writeOperation(ec.owner, ctx, true, func(opCtx context.Context) (CallResult, error) {
		if tx == nil {
			return CallResult{}, fmt.Errorf("%w: nil transaction", rpc.ErrInvalidRequest)
		}
		raw, err := tx.MarshalBinary()
		if err != nil {
			return CallResult{}, fmt.Errorf("%w: encode transaction: %v", rpc.ErrInvalidRequest, err)
		}
		return ec.owner.sendRawTransaction(opCtx, raw)
	})
	return err
}

func (ec *EthClient) SendTransactionSync(ctx context.Context, tx *types.Transaction, timeout *time.Duration) (*types.Receipt, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Receipt, CallResult, error) {
		if tx == nil {
			return nil, CallResult{}, fmt.Errorf("%w: nil transaction", rpc.ErrInvalidRequest)
		}
		raw, marshalErr := tx.MarshalBinary()
		if marshalErr != nil {
			return nil, CallResult{}, fmt.Errorf("%w: encode transaction: %v", rpc.ErrInvalidRequest, marshalErr)
		}
		return ec.owner.sendRawTransactionSyncExec(opCtx, raw, timeout)
	})
	return value, err
}

func (ec *EthClient) SendRawTransactionSync(ctx context.Context, rawTx []byte, timeout *time.Duration) (*types.Receipt, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*types.Receipt, CallResult, error) {
		return ec.owner.sendRawTransactionSyncExec(opCtx, rawTx, timeout)
	})
	return value, err
}

// PendingBalanceAt returns the node-local pending balance.
func (ec *EthClient) PendingBalanceAt(ctx context.Context, account common.Address) (*big.Int, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return ec.owner.pendingBalanceAtExec(opCtx, account)
	})
	return value, err
}

func (ec *EthClient) PendingStorageAt(ctx context.Context, account common.Address, key common.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.pendingStorageAtExec(opCtx, account, key)
	})
	return value, err
}

func (ec *EthClient) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.pendingCodeAtExec(opCtx, account)
	})
	return value, err
}

func (ec *EthClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.pendingNonceAtExec(opCtx, account)
	})
	return value, err
}

func (ec *EthClient) CallContractAtHash(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.callContractAtHashExec(opCtx, msg, blockHash)
	})
	return value, err
}

func (ec *EthClient) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.pendingCallContractExec(opCtx, msg)
	})
	return value, err
}

func (ec *EthClient) EstimateGasAtBlock(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		arg, localErr := checkedBlockNumArg(blockNumber)
		if localErr != nil {
			return 0, CallResult{Err: localErr}, localErr
		}
		return ec.owner.estimateGasWithArgExec(opCtx, toCallArg(msg), arg)
	})
	return value, err
}

func (ec *EthClient) EstimateGasAtBlockHash(ctx context.Context, msg ethereum.CallMsg, blockHash common.Hash) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.estimateGasWithArgExec(opCtx, toCallArg(msg), hashBlockArg(blockHash))
	})
	return value, err
}

func (ec *EthClient) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	return ec.owner.SubscribeNewHead(ctx, ch)
}

func (ec *EthClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	return ec.owner.SubscribeFilterLogs(ctx, q, ch)
}

func (ec *EthClient) SubscribeTransactionReceipts(ctx context.Context, q *ethereum.TransactionReceiptsQuery, ch chan<- []*types.Receipt) (ethereum.Subscription, error) {
	return ec.owner.SubscribeTransactionReceipts(ctx, q, ch)
}

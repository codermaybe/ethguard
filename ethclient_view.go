package ethguard

import (
	"context"
	"math/big"
	"time"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// EthClient is the ethclient-shaped view of one Client: the official
// go-ethereum read signatures, synchronous write methods, subscription
// methods, and Close.
// It holds a private owner; it is never an embedded facade, and it adds no
// anchor, Call, Pool, Mode, Normalize, raw-write, or CreateAccessList
// surface. The view performs no networking of its own and builds no second
// pool: every method is a thin projection of the same shared execution path
// the native methods run, with the view flag telling the entry to omit raw
// material collection when no ResultCapture is bound (nothing would consume
// it). No pointer-identity or zero-allocation promise is made. It is not
// go-ethereum's concrete *ethclient.Client. After the public type migration
// its scalar entries take the generated ethapi.Address/ethapi.Hash and its
// object entries return the generated models, so bind/v2's DeployBackend and
// ContractCaller are intentionally no longer satisfied; call inputs remain
// the project's generated ethapi.CallMsg. Both facts are checked by the API
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
func (ec *EthClient) BalanceAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (*big.Int, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return ec.owner.balanceAtExec(opCtx, account, blockNumber)
	})
	return value, err
}

// BalanceAtHash returns the corroborated balance of account at blockHash.
func (ec *EthClient) BalanceAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (*big.Int, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return ec.owner.balanceAtHashExec(opCtx, account, blockHash)
	})
	return value, err
}

// NonceAt returns the corroborated account nonce of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference.
func (ec *EthClient) NonceAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.nonceAtExec(opCtx, account, blockNumber)
	})
	return value, err
}

// NonceAtHash returns the corroborated account nonce at blockHash.
func (ec *EthClient) NonceAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.nonceAtHashExec(opCtx, account, blockHash)
	})
	return value, err
}

// StorageAt returns the corroborated value of key in account's storage at
// blockNumber; nil pins the moving tag to the currently corroborated reference.
func (ec *EthClient) StorageAt(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockNumber *big.Int) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.storageAtExec(opCtx, account, key, blockNumber)
	})
	return value, err
}

// StorageAtHash returns the corroborated storage value at blockHash.
func (ec *EthClient) StorageAtHash(ctx context.Context, account ethapi.Address, key ethapi.Hash, blockHash ethapi.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.storageAtHashExec(opCtx, account, key, blockHash)
	})
	return value, err
}

// CodeAt returns the corroborated contract code of account at blockNumber;
// nil pins the moving tag to the currently corroborated reference. An
// externally owned account answers empty code, not an error.
func (ec *EthClient) CodeAt(ctx context.Context, account ethapi.Address, blockNumber *big.Int) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.codeAtExec(opCtx, account, blockNumber)
	})
	return value, err
}

// CodeAtHash returns the corroborated contract code at blockHash.
func (ec *EthClient) CodeAtHash(ctx context.Context, account ethapi.Address, blockHash ethapi.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.codeAtHashExec(opCtx, account, blockHash)
	})
	return value, err
}

// BlockByHash returns the corroborated block with the given hash.
func (ec *EthClient) BlockByHash(ctx context.Context, hash ethapi.Hash) (*ethapi.Block, error) {
	block, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Block, CallResult, error) {
		return ec.owner.blockByHashExec(opCtx, hash)
	})
	return block, err
}

// HeaderByHash returns the corroborated header with the given hash.
func (ec *EthClient) HeaderByHash(ctx context.Context, hash ethapi.Hash) (*ethapi.Header, error) {
	header, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Header, CallResult, error) {
		return ec.owner.headerByHashExec(opCtx, hash)
	})
	return header, err
}

// BlockByNumber returns the corroborated block at number, with go-ethereum's
// negative-tag contract: -1 pending is rejected locally, -2/-3/-4 establish
// their own level's reference, -5 is earliest (height 0), nil pins the
// currently corroborated reference.
func (ec *EthClient) BlockByNumber(ctx context.Context, number *big.Int) (*ethapi.Block, error) {
	block, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Block, CallResult, error) {
		return ec.owner.blockByNumberExec(opCtx, number)
	})
	return block, err
}

// HeaderByNumber returns a corroborated header at number. The moving-tag and
// pending rules match the root Client's HeaderByNumber contract.
func (ec *EthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*ethapi.Header, error) {
	header, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Header, CallResult, error) {
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
func (ec *EthClient) BlockReceipts(ctx context.Context, blockRef ethapi.BlockReference) ([]ethapi.Receipt, error) {
	receipts, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]ethapi.Receipt, CallResult, error) {
		return ec.owner.blockReceiptsExec(opCtx, blockRef)
	})
	return receipts, err
}

// TransactionReceipt returns the fleet-corroborated receipt for the
// transaction hash; ethereum.NotFound when the fleet corroborates the
// transaction has no receipt yet.
func (ec *EthClient) TransactionReceipt(ctx context.Context, hash ethapi.Hash) (*ethapi.Receipt, error) {
	receipt, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Receipt, CallResult, error) {
		return ec.owner.transactionReceiptExec(opCtx, hash)
	})
	return receipt, err
}

// TransactionByHash returns the fleet-corroborated transaction for the hash
// and whether the fleet saw it as still pending; ethereum.NotFound when the
// fleet corroborates no such transaction exists.
func (ec *EthClient) TransactionByHash(ctx context.Context, hash ethapi.Hash) (*ethapi.Transaction, bool, error) {
	tx, pending, _, err := operationPair(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Transaction, bool, CallResult, error) {
		return ec.owner.transactionByHashExec(opCtx, hash)
	})
	return tx, pending, err
}

// TransactionCount returns the fleet-corroborated transaction count for an
// immutable block hash. A corroborated JSON null maps to ethereum.NotFound.
func (ec *EthClient) TransactionCount(ctx context.Context, blockHash ethapi.Hash) (uint, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint, CallResult, error) {
		return ec.owner.transactionCountExec(opCtx, blockHash)
	})
	return value, err
}

// TransactionInBlock returns the transaction at an index in an immutable
// block, or ethereum.NotFound when the block/index has no transaction.
func (ec *EthClient) TransactionInBlock(ctx context.Context, blockHash ethapi.Hash, index uint) (*ethapi.Transaction, error) {
	tx, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Transaction, CallResult, error) {
		return ec.owner.transactionInBlockExec(opCtx, blockHash, index)
	})
	return tx, err
}

// TransactionSender returns the sender reported for the transaction at the
// requested immutable block/index. The input is the same generated public
// transaction the query methods return; the sender is the RPC-declared
// sender at this decision's trust level.
func (ec *EthClient) TransactionSender(ctx context.Context, tx *ethapi.Transaction, blockHash ethapi.Hash, index uint) (ethapi.Address, error) {
	sender, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (ethapi.Address, CallResult, error) {
		return ec.owner.transactionSenderExec(opCtx, tx, blockHash, index)
	})
	return sender, err
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
func (ec *EthClient) CallContract(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) ([]byte, error) {
	data, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.callContractExec(opCtx, msg, blockNumber)
	})
	return data, err
}

// EstimateGas returns the fleet's lower-median gas estimate for msg at the
// currently corroborated reference — a variable result, Selected at best.
func (ec *EthClient) EstimateGas(ctx context.Context, msg ethapi.CallMsg) (uint64, error) {
	gas, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.estimateGasExec(opCtx, msg)
	})
	return gas, err
}

// FilterLogs returns the fleet-corroborated logs matching q. Range filters
// are pinned to the corroborated reference like any other anchored read.
func (ec *EthClient) FilterLogs(ctx context.Context, q ethapi.FilterQuery) ([]ethapi.Log, error) {
	logs, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]ethapi.Log, CallResult, error) {
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
func (ec *EthClient) SyncProgress(ctx context.Context) (*ethapi.SyncProgress, error) {
	progress, _, err := operation(ec.owner, ctx, true, ec.owner.syncProgressExec)
	return progress, err
}

// FeeHistory returns the fleet-corroborated fee history ending at the
// corroborated reference: lastBlock nil pins the moving "latest" tag.
func (ec *EthClient) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (*ethapi.FeeHistoryResult, error) {
	history, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.FeeHistoryResult, CallResult, error) {
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
// the signed transaction once through the BinaryTransaction contract and
// broadcast its raw bytes. The completion result still publishes to a bound
// ResultCapture — the error is the view's only direct return.
func (ec *EthClient) SendTransaction(ctx context.Context, tx BinaryTransaction) error {
	_, err := writeOperation(ec.owner, ctx, true, func(opCtx context.Context) (CallResult, error) {
		return ec.owner.sendTransactionExec(opCtx, tx)
	})
	return err
}

func (ec *EthClient) SendTransactionSync(ctx context.Context, tx BinaryTransaction, timeout *time.Duration) (*ethapi.Receipt, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Receipt, CallResult, error) {
		return ec.owner.sendTransactionSyncExec(opCtx, tx, timeout)
	})
	return value, err
}

func (ec *EthClient) SendRawTransactionSync(ctx context.Context, rawTx []byte, timeout *time.Duration) (*ethapi.Receipt, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*ethapi.Receipt, CallResult, error) {
		return ec.owner.sendRawTransactionSyncExec(opCtx, rawTx, timeout)
	})
	return value, err
}

// PendingBalanceAt returns the node-local pending balance.
func (ec *EthClient) PendingBalanceAt(ctx context.Context, account ethapi.Address) (*big.Int, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return ec.owner.pendingBalanceAtExec(opCtx, account)
	})
	return value, err
}

func (ec *EthClient) PendingStorageAt(ctx context.Context, account ethapi.Address, key ethapi.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.pendingStorageAtExec(opCtx, account, key)
	})
	return value, err
}

func (ec *EthClient) PendingCodeAt(ctx context.Context, account ethapi.Address) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.pendingCodeAtExec(opCtx, account)
	})
	return value, err
}

func (ec *EthClient) PendingNonceAt(ctx context.Context, account ethapi.Address) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.pendingNonceAtExec(opCtx, account)
	})
	return value, err
}

func (ec *EthClient) CallContractAtHash(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.callContractAtHashExec(opCtx, msg, blockHash)
	})
	return value, err
}

func (ec *EthClient) PendingCallContract(ctx context.Context, msg ethapi.CallMsg) ([]byte, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) ([]byte, CallResult, error) {
		return ec.owner.pendingCallContractExec(opCtx, msg)
	})
	return value, err
}

func (ec *EthClient) EstimateGasAtBlock(ctx context.Context, msg ethapi.CallMsg, blockNumber *big.Int) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.estimateGasAtBlockExec(opCtx, msg, blockNumber)
	})
	return value, err
}

func (ec *EthClient) EstimateGasAtBlockHash(ctx context.Context, msg ethapi.CallMsg, blockHash ethapi.Hash) (uint64, error) {
	value, _, err := operation(ec.owner, ctx, true, func(opCtx context.Context) (uint64, CallResult, error) {
		return ec.owner.estimateGasAtBlockHashExec(opCtx, msg, blockHash)
	})
	return value, err
}

func (ec *EthClient) SubscribeNewHead(ctx context.Context, policy HeadSubscriptionPolicy) (*HeadSubscription, error) {
	return ec.owner.SubscribeNewHead(ctx, policy)
}

func (ec *EthClient) SubscribeFilterLogs(ctx context.Context, q ethapi.FilterQuery, policy LogsSubscriptionPolicy) (*LogsSubscription, error) {
	return ec.owner.SubscribeFilterLogs(ctx, q, policy)
}

func (ec *EthClient) SubscribeTransactionReceipts(ctx context.Context, q *ethapi.TransactionReceiptsQuery, policy ReceiptsSubscriptionPolicy) (*ReceiptsSubscription, error) {
	return ec.owner.SubscribeTransactionReceipts(ctx, q, policy)
}

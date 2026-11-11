package methods

// The remaining centralized method-name declarations. Migrated methods own
// their spellings alongside their specs and descriptors; each RPC spelling
// still has exactly one declaration. The write method has no read policy.

// MethodCall is the JSON-RPC method name resolved by CallPolicy.
const MethodCall = "eth_call"

// MethodCreateAccessList is the JSON-RPC method name resolved by
// CreateAccessListPolicy.
const MethodCreateAccessList = "eth_createAccessList"

// MethodEstimateGas is the JSON-RPC method name resolved by
// EstimateGasPolicy.
const MethodEstimateGas = "eth_estimateGas"

// MethodFeeHistory is the JSON-RPC method name resolved by FeeHistoryPolicy.
const MethodFeeHistory = "eth_feeHistory"

// MethodGetBalance is the JSON-RPC method name resolved by GetBalancePolicy.
const MethodGetBalance = "eth_getBalance"

// MethodGetBlockByHash is the JSON-RPC method name resolved by
// BlockByHashPolicy.
const MethodGetBlockByHash = "eth_getBlockByHash"

// MethodGetBlockByNumber is the JSON-RPC method name resolved by
// BlockByNumberPolicy.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// MethodGetBlockReceipts is the JSON-RPC method name resolved by
// BlockReceiptsPolicy.
const MethodGetBlockReceipts = "eth_getBlockReceipts"

// MethodGetCode is the JSON-RPC method name resolved by GetCodePolicy.
const MethodGetCode = "eth_getCode"

// MethodGetLogs is the JSON-RPC method name resolved by LogsPolicy.
const MethodGetLogs = "eth_getLogs"

// MethodGetStorageAt is the JSON-RPC method name resolved by
// GetStorageAtPolicy.
const MethodGetStorageAt = "eth_getStorageAt"

// MethodGetTransactionCount is the JSON-RPC method name resolved by
// GetTransactionCountPolicy.
const MethodGetTransactionCount = "eth_getTransactionCount"

// MethodGetBlockTransactionCountByHash is the JSON-RPC method resolved by
// TransactionCount. It counts transactions in one immutable block and is
// distinct from eth_getTransactionCount, which returns an account nonce.
const MethodGetBlockTransactionCountByHash = "eth_getBlockTransactionCountByHash"

// MethodGetBlockTransactionCountByNumber is the JSON-RPC method name resolved
// by the block transaction-count policy.
const MethodGetBlockTransactionCountByNumber = "eth_getBlockTransactionCountByNumber"

// MethodGetTransactionReceipt is the JSON-RPC method name resolved by
// TransactionReceiptPolicy.
const MethodGetTransactionReceipt = "eth_getTransactionReceipt"

// MethodSyncing is the JSON-RPC method name resolved by SyncingPolicy.
const MethodSyncing = "eth_syncing"

// MethodSendRawTransaction is the write-side JSON-RPC method. It has no
// Policy in the read registry: the write pipeline owns its collection and
// adjudication (pool.Broadcast + relative-trust resolution), not the
// method-policy registry.
const MethodSendRawTransaction = "eth_sendRawTransaction"

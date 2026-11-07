package methods

// The method-name table: one constant per JSON-RPC method this package owns.
// The spec inventory (spec_inventory.go), the catalog (catalog.go), and the
// legacy constructors all reference these names; the table is the single
// spelling authority. Twenty-three read methods are registered; the
// transaction write method has no read policy.

// MethodBlobBaseFee is the JSON-RPC method name resolved by BlobBaseFeePolicy.
const MethodBlobBaseFee = "eth_blobBaseFee"

// MethodBlockNumber is the JSON-RPC method name resolved by BlockNumberPolicy.
const MethodBlockNumber = "eth_blockNumber"

// MethodCall is the JSON-RPC method name resolved by CallPolicy.
const MethodCall = "eth_call"

// MethodChainID is the JSON-RPC method name resolved by ChainIDPolicy.
const MethodChainID = "eth_chainId"

// MethodCreateAccessList is the JSON-RPC method name resolved by
// CreateAccessListPolicy.
const MethodCreateAccessList = "eth_createAccessList"

// MethodEstimateGas is the JSON-RPC method name resolved by
// EstimateGasPolicy.
const MethodEstimateGas = "eth_estimateGas"

// MethodFeeHistory is the JSON-RPC method name resolved by FeeHistoryPolicy.
const MethodFeeHistory = "eth_feeHistory"

// MethodGasPrice is the JSON-RPC method name resolved by GasPricePolicy.
const MethodGasPrice = "eth_gasPrice"

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

// MethodGetTransactionByHash is the JSON-RPC method name resolved by
// TransactionByHashPolicy.
const MethodGetTransactionByHash = "eth_getTransactionByHash"

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

// MethodGetTransactionByBlockHashAndIndex is resolved by TransactionInBlock.
const MethodGetTransactionByBlockHashAndIndex = "eth_getTransactionByBlockHashAndIndex"

// MethodGetTransactionReceipt is the JSON-RPC method name resolved by
// TransactionReceiptPolicy.
const MethodGetTransactionReceipt = "eth_getTransactionReceipt"

// MethodListening is the JSON-RPC method name resolved by ListeningPolicy.
const MethodListening = "net_listening"

// MethodMaxPriorityFeePerGas is the JSON-RPC method name resolved by
// MaxPriorityFeePerGasPolicy.
const MethodMaxPriorityFeePerGas = "eth_maxPriorityFeePerGas"

// MethodPeerCount is the JSON-RPC method name resolved by PeerCountPolicy.
// Note the namespace: this is a net_ method, corroborated like any other
// registered method.
const MethodPeerCount = "net_peerCount"

// MethodSyncing is the JSON-RPC method name resolved by SyncingPolicy.
const MethodSyncing = "eth_syncing"

// MethodNetVersion is the JSON-RPC method name resolved by NetVersionPolicy.
const MethodNetVersion = "net_version"

// MethodSendRawTransaction is the write-side JSON-RPC method. It has no
// Policy in the read registry: the write pipeline owns its collection and
// adjudication (pool.Broadcast + relative-trust resolution), not the
// method-policy registry.
const MethodSendRawTransaction = "eth_sendRawTransaction"

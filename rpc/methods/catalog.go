package methods

import (
	"fmt"

	"ethguard/rpc"
)

// MethodKind describes how a JSON-RPC method is exposed by EthGuard.  A
// registered method has a method-aware policy in this package.  Derived
// methods are intentionally absent from the RPC registry because the root
// client obtains their value from an already established reference.  Known
// unsupported methods are listed explicitly so an omission cannot be mistaken
// for a transparent verification guarantee.  Write methods are supported
// through the dedicated write pipeline outside the read registry, so they
// carry no read-registry policy.
type MethodKind uint8

const (
	RegisteredMethod MethodKind = iota
	DerivedMethod
	UnsupportedMethod
	WriteMethod
)

// MethodDescriptor is the public inventory of method coverage.  Adapter is
// the ethclient-shaped method when one exists; an empty Adapter means callers
// use Client.Call or the root Client wrapper instead.
type MethodDescriptor struct {
	Name    string
	Kind    MethodKind
	Policy  string
	Adapter string
	Notes   string
}

// Catalog returns a fresh copy of the method coverage inventory.  Returning a
// copy keeps callers from mutating package-level coverage metadata while
// still allowing tooling and documentation checks to enumerate the registry.
func Catalog() []MethodDescriptor {
	return append([]MethodDescriptor(nil), methodCatalog...)
}

// RegisteredNames returns the methods that have method-aware policies in the
// default ethguard registry.  The order is stable and follows client.go's
// construction order so diagnostics remain deterministic.
func RegisteredNames() []string {
	names := make([]string, 0, len(methodCatalog))
	for _, method := range methodCatalog {
		if method.Kind == RegisteredMethod {
			names = append(names, method.Name)
		}
	}
	return names
}

// ValidateRegistry checks that the default registry has a policy for every
// registered descriptor. It intentionally permits additional private entries
// so callers can extend a registry without changing the public inventory.
func ValidateRegistry(registry rpc.Registry) error {
	for _, method := range methodCatalog {
		if method.Kind != RegisteredMethod {
			continue
		}
		if registry[method.Name] == nil {
			return fmt.Errorf("method catalog: registered method %q has no policy", method.Name)
		}
	}
	return nil
}

var methodCatalog = []MethodDescriptor{
	{Name: MethodChainID, Kind: RegisteredMethod, Policy: "ChainIDPolicy", Adapter: "ChainID"},
	{Name: MethodGetBlockByHash, Kind: RegisteredMethod, Policy: "BlockByHashPolicy", Adapter: "BlockByHash"},
	{Name: MethodGetBlockByNumber, Kind: RegisteredMethod, Policy: "BlockByNumberPolicy", Adapter: "BlockByNumber"},
	{Name: MethodGetBalance, Kind: RegisteredMethod, Policy: "GetBalancePolicy", Adapter: "BalanceAt"},
	{Name: MethodGetTransactionReceipt, Kind: RegisteredMethod, Policy: "TransactionReceiptPolicy", Adapter: "TransactionReceipt"},
	{Name: MethodGetTransactionByHash, Kind: RegisteredMethod, Policy: "TransactionByHashPolicy", Adapter: "TransactionByHash"},
	{Name: MethodGetBlockReceipts, Kind: RegisteredMethod, Policy: "BlockReceiptsPolicy", Adapter: "BlockReceipts"},
	{Name: MethodGetTransactionCount, Kind: RegisteredMethod, Policy: "GetTransactionCountPolicy", Adapter: "NonceAt"},
	{Name: MethodGetStorageAt, Kind: RegisteredMethod, Policy: "GetStorageAtPolicy", Adapter: "StorageAt"},
	{Name: MethodGetCode, Kind: RegisteredMethod, Policy: "GetCodePolicy", Adapter: "CodeAt"},
	{Name: MethodCall, Kind: RegisteredMethod, Policy: "CallPolicy", Adapter: "CallContract"},
	{Name: MethodEstimateGas, Kind: RegisteredMethod, Policy: "EstimateGasPolicy", Adapter: "EstimateGas"},
	{Name: MethodGetLogs, Kind: RegisteredMethod, Policy: "LogsPolicy", Adapter: "FilterLogs"},
	{Name: MethodFeeHistory, Kind: RegisteredMethod, Policy: "FeeHistoryPolicy"},
	{Name: MethodGasPrice, Kind: RegisteredMethod, Policy: "GasPricePolicy", Adapter: "SuggestGasPrice"},
	{Name: MethodMaxPriorityFeePerGas, Kind: RegisteredMethod, Policy: "MaxPriorityFeePerGasPolicy", Adapter: "SuggestGasTipCap"},
	{Name: MethodBlobBaseFee, Kind: RegisteredMethod, Policy: "BlobBaseFeePolicy"},
	{Name: MethodBlockNumber, Kind: RegisteredMethod, Policy: "BlockNumberPolicy", Adapter: "BlockNumber"},
	{Name: MethodCreateAccessList, Kind: RegisteredMethod, Policy: "CreateAccessListPolicy"},
	{Name: MethodPeerCount, Kind: RegisteredMethod, Policy: "PeerCountPolicy", Adapter: "PeerCount"},
	{Name: MethodSyncing, Kind: RegisteredMethod, Policy: "SyncingPolicy", Adapter: "SyncProgress"},
	{Name: MethodNetVersion, Kind: RegisteredMethod, Policy: "NetVersionPolicy", Notes: "Root Client.NetVersion; adapter has no ethclient-shaped method."},
	{Name: MethodListening, Kind: RegisteredMethod, Policy: "ListeningPolicy", Notes: "Root Client.Listening; adapter has no ethclient-shaped method."},
	// Write path (decision-map #4): a dedicated pipeline outside the read
	// registry. Broadcast to the full voting basis + relative-trust
	// adjudication; WriteMethod entries are skipped by ValidateRegistry.
	{Name: MethodSendRawTransaction, Kind: WriteMethod, Policy: "WritePipeline", Adapter: "SendTransaction", Notes: "Root Client.SendRawTransaction; broadcast + relative-trust, no read registry policy."},
	{Name: "eth_getProof", Kind: UnsupportedMethod, Notes: "Not registered; no account/storage proof admission contract yet."},
}

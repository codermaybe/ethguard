package methods

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// MethodKind describes how a JSON-RPC method is exposed by EthGuard.  A
// registered method has a method-aware policy in this package.  Derived
// methods are intentionally absent from the RPC registry because the root
// client obtains their value from an already established reference.  Known
// unsupported methods are listed explicitly so an omission cannot be mistaken
// for a transparent corroboration guarantee.  Write methods are supported
// through the dedicated write pipeline outside the read registry, so they
// carry no read-registry policy.
type MethodKind uint8

const (
	RegisteredMethod MethodKind = iota
	DerivedMethod
	UnsupportedMethod
	WriteMethod
)

// MethodFamily is the execution family a method belongs to. The family —
// not the method count — decides the execution model: read runs the
// one-shot fan-out pipeline, write runs the broadcast pipeline, stream and
// verification are separate families, and derivation needs no RPC.
type MethodFamily string

const (
	FamilyRead         MethodFamily = "read"
	FamilyWrite        MethodFamily = "write"
	FamilyStream       MethodFamily = "stream"
	FamilyVerification MethodFamily = "verification"
	FamilyDerivation   MethodFamily = "derivation"
)

// MethodCoverage is the independent coverage classification: registered
// methods have a method-aware policy, unsupported methods are listed
// explicitly so an omission cannot masquerade as a guarantee, and forwarded
// methods are unregistered transparent passthroughs. It deliberately does
// not extend MethodKind: Kind is the registry shape, Coverage is the
// safety-contract evidence state.
type MethodCoverage string

const (
	CoverageRegistered  MethodCoverage = "registered"
	CoverageUnsupported MethodCoverage = "unsupported"
	CoverageForwarded   MethodCoverage = "forwarded"
)

// MethodDescriptor is the public inventory of method coverage.  Adapter is
// the ethclient-shaped method when one exists; an empty Adapter means callers
// use Client.Call or the root Client wrapper instead. Alignment records the
// go-ethereum version the adapter mapping was last reviewed against.
type MethodDescriptor struct {
	Name      string
	Kind      MethodKind
	Family    MethodFamily
	Coverage  MethodCoverage
	Policy    string
	Adapter   string
	Alignment string
	Notes     string
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

// ValidateReadCoverage checks that every registered descriptor is driven by
// either a compiled policy or an explicit execution plan. It is the
// migration-era coverage gate: as methods move to plans their policies leave
// the registry, and a method that lost both shapes fails loudly instead of
// falling through to transparent forwarding.
func ValidateReadCoverage(policies rpc.Registry, plans map[string]rpc.ReadPlan) error {
	for _, method := range methodCatalog {
		if method.Kind != RegisteredMethod {
			continue
		}
		if policies[method.Name] == nil && plans[method.Name].Resolver == nil {
			return fmt.Errorf("method catalog: registered method %q has neither a policy nor a plan", method.Name)
		}
	}
	return nil
}

var methodCatalog = []MethodDescriptor{
	{Name: MethodChainID, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "ChainIDPolicy", Adapter: "ChainID", Alignment: "v1.17.5"},
	{Name: MethodGetBlockByHash, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "BlockByHashPolicy", Adapter: "BlockByHash", Alignment: "v1.17.5"},
	{Name: MethodGetBlockByNumber, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "BlockByNumberPolicy", Adapter: "BlockByNumber", Alignment: "v1.17.5"},
	{Name: MethodGetBalance, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "GetBalancePolicy", Adapter: "BalanceAt", Alignment: "v1.17.5"},
	{Name: MethodGetTransactionReceipt, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "TransactionReceiptPolicy", Adapter: "TransactionReceipt", Alignment: "v1.17.5"},
	{Name: MethodGetTransactionByHash, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "TransactionByHashPolicy", Adapter: "TransactionByHash", Alignment: "v1.17.5"},
	{Name: MethodGetBlockReceipts, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "BlockReceiptsPolicy", Adapter: "BlockReceipts", Alignment: "v1.17.5"},
	{Name: MethodGetTransactionCount, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "GetTransactionCountPolicy", Adapter: "NonceAt", Alignment: "v1.17.5"},
	{Name: MethodGetStorageAt, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "GetStorageAtPolicy", Adapter: "StorageAt", Alignment: "v1.17.5"},
	{Name: MethodGetCode, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "GetCodePolicy", Adapter: "CodeAt", Alignment: "v1.17.5"},
	{Name: MethodCall, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "CallPolicy", Adapter: "CallContract", Alignment: "v1.17.5"},
	{Name: MethodEstimateGas, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "EstimateGasPolicy", Adapter: "EstimateGas", Alignment: "v1.17.5"},
	{Name: MethodGetLogs, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "LogsPolicy", Adapter: "FilterLogs", Alignment: "v1.17.5"},
	{Name: MethodFeeHistory, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "FeeHistoryPolicy", Alignment: "v1.17.5"},
	{Name: MethodGasPrice, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "GasPricePolicy", Adapter: "SuggestGasPrice", Alignment: "v1.17.5"},
	{Name: MethodMaxPriorityFeePerGas, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "MaxPriorityFeePerGasPolicy", Adapter: "SuggestGasTipCap", Alignment: "v1.17.5"},
	{Name: MethodBlobBaseFee, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "BlobBaseFeePolicy", Alignment: "v1.17.5"},
	{Name: MethodBlockNumber, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "BlockNumberPolicy", Adapter: "BlockNumber", Alignment: "v1.17.5"},
	{Name: MethodCreateAccessList, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "CreateAccessListPolicy", Alignment: "v1.17.5"},
	{Name: MethodPeerCount, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "PeerCountPolicy", Adapter: "PeerCount", Alignment: "v1.17.5"},
	{Name: MethodSyncing, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "SyncingPolicy", Adapter: "SyncProgress", Alignment: "v1.17.5"},
	{Name: MethodNetVersion, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "NetVersionPolicy", Notes: "Root Client.NetVersion; adapter has no ethclient-shaped method."},
	{Name: MethodListening, Kind: RegisteredMethod, Family: FamilyRead, Coverage: CoverageRegistered, Policy: "ListeningPolicy", Notes: "Root Client.Listening; adapter has no ethclient-shaped method."},
	// Write path (decision-map #4): a dedicated pipeline outside the read
	// registry. Broadcast to the full voting basis + relative-trust
	// adjudication; WriteMethod entries are skipped by ValidateRegistry.
	{Name: MethodSendRawTransaction, Kind: WriteMethod, Family: FamilyWrite, Coverage: CoverageRegistered, Policy: "WritePipeline", Adapter: "SendTransaction", Alignment: "v1.17.5", Notes: "Root Client.SendRawTransaction; broadcast + relative-trust, no read registry policy."},
	// Verification family: eth_getProof stays explicitly unsupported until a
	// real-provider evidence contract exists (proposal section 8); it is
	// never a silent omission.
	{Name: "eth_getProof", Kind: UnsupportedMethod, Family: FamilyVerification, Coverage: CoverageUnsupported, Notes: "Not registered; no account/storage proof admission contract yet."},
	// Stream family: subscriptions are deferred until the native WSS design
	// lands; the transport Port deliberately carries no Subscribe (frozen
	// decision D12/D13).
	// Derivation family: TransactionSender and header projection are local
	// derivations on the facade side; they register no RPC policy.
}

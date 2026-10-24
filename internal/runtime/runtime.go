// Package runtime owns the production composition of the RPC method catalog,
// typed observation projectors, and aggregate execution pipeline.
//
// It deliberately does not expose a public client and does not construct
// references or make business decisions. The root facade supplies the small
// reference functions needed by method policies; this keeps the package
// independent from the facade while making method binding visible in the
// single Composition Root.
package runtime

import (
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpc/projection"
)

// Inputs are the non-business dependencies required to assemble one client
// runtime. Anchor functions are supplied by the reference owner; runtime only
// binds them to the methods that need them.
type Inputs struct {
	Pool           *pool.Pool
	AnchorHash     rpc.AnchorFunc
	AnchorHeight   rpc.AnchorHeightFunc
	StrictOptions  []rpc.StrictOption
	Recorder       observe.Recorder
	CollectTimeout time.Duration
	Corroboration  *rpc.CorroborationShared
}

// Bundle is the internal execution graph consumed by the root facade. It is
// intentionally data-only: callers still use the facade's public Client.
type Bundle struct {
	Policies   rpc.Registry
	Aggregator *aggregator.Aggregator
}

// Build assembles the method catalog, typed observation projectors, and the
// aggregator. Method policy binding lives here; reference establishment and
// policy implementation remain owned by their respective modules.
func Build(in Inputs) (*Bundle, error) {
	if in.Pool == nil {
		return nil, fmt.Errorf("runtime: nil pool")
	}
	policies := rpc.Registry{
		methods.MethodChainID:               methods.ChainIDPolicy(in.StrictOptions...),
		methods.MethodGetBlockByHash:        methods.BlockByHashPolicy(in.StrictOptions...),
		methods.MethodGetBlockByNumber:      methods.BlockByNumberPolicy(in.StrictOptions...),
		methods.MethodGetBalance:            methods.GetBalancePolicy(in.AnchorHash, in.StrictOptions...),
		methods.MethodGetTransactionReceipt: methods.TransactionReceiptPolicy(in.StrictOptions...),
		methods.MethodGetTransactionByHash:  methods.TransactionByHashPolicy(in.StrictOptions...),
		methods.MethodGetBlockReceipts:      methods.BlockReceiptsPolicy(in.AnchorHash, in.StrictOptions...),
		methods.MethodGetTransactionCount:   methods.GetTransactionCountPolicy(in.AnchorHash, in.StrictOptions...),
		methods.MethodGetStorageAt:          methods.GetStorageAtPolicy(in.AnchorHash, in.StrictOptions...),
		methods.MethodGetCode:               methods.GetCodePolicy(in.AnchorHash, in.StrictOptions...),
		methods.MethodCall:                  methods.CallPolicy(in.AnchorHash, in.StrictOptions...),
		methods.MethodEstimateGas:           methods.EstimateGasPolicy(in.AnchorHash),
		methods.MethodGetLogs:               methods.LogsPolicy(in.AnchorHeight, in.StrictOptions...),
		methods.MethodFeeHistory:            methods.FeeHistoryPolicy(in.AnchorHeight, in.StrictOptions...),
		methods.MethodGasPrice:              methods.GasPricePolicy(),
		methods.MethodMaxPriorityFeePerGas:  methods.MaxPriorityFeePerGasPolicy(),
		methods.MethodBlobBaseFee:           methods.BlobBaseFeePolicy(),
		methods.MethodBlockNumber:           methods.BlockNumberPolicy(),
		methods.MethodCreateAccessList:      methods.CreateAccessListPolicy(in.AnchorHash),
		methods.MethodPeerCount:             methods.PeerCountPolicy(),
		methods.MethodSyncing:               methods.SyncingPolicy(),
		methods.MethodNetVersion:            methods.NetVersionPolicy(in.StrictOptions...),
		methods.MethodListening:             methods.ListeningPolicy(),
	}
	if err := methods.ValidateRegistry(policies); err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	projectors := map[string]rpc.ObservationProjector{
		methods.MethodGetBalance: projection.ScalarStandardProjector(),
		methods.MethodCall:       projection.ScalarStandardProjector(),
	}
	for _, method := range []string{
		methods.MethodGetTransactionReceipt,
		methods.MethodGetBlockReceipts,
		methods.MethodGetBlockByHash,
		methods.MethodGetBlockByNumber,
		methods.MethodGetTransactionByHash,
		methods.MethodGetLogs,
		methods.MethodFeeHistory,
	} {
		projector, err := projection.NewSchemaProjector(method)
		if err != nil {
			return nil, fmt.Errorf("runtime: build %s projector: %w", method, err)
		}
		projectors[method] = projector
	}
	return &Bundle{
		Policies: policies,
		Aggregator: aggregator.New(in.Pool, policies,
			aggregator.WithRecorder(in.Recorder),
			aggregator.WithCollectTimeout(in.CollectTimeout),
			aggregator.WithCorroboration(in.Corroboration),
			aggregator.WithProjectors(projectors)),
	}, nil
}

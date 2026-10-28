// Package runtime owns the production composition of the RPC method catalog,
// typed observation projectors, the reference establishment stage, and the
// aggregate execution pipeline.
//
// It deliberately does not expose a public client. Method binding, the
// reference stage, and the anchor readers all live here, which makes the
// assembly visible in the single Composition Root: method policies receive
// consumption-only anchor readers bound to the stage, so no method holds a
// callback that fans out its own network round.
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

// AnchoringStrategy selects the establishment strategy for moving-tag reads.
// The zero value keeps the strict-head strategy for every tag.
type AnchoringStrategy struct {
	// Finalized establishes the finalized tag through the candidate-selection
	// algorithm instead of the strict head algorithm.
	Finalized bool
	// MaxLag bounds how far a reported finalized view may trail the fleet's
	// lower median (finalized strategy only).
	MaxLag uint64
}

// Inputs are the non-business dependencies required to assemble one client
// runtime. The reference stage is constructed here; method policies receive
// its consumption-only anchor readers.
type Inputs struct {
	Pool           *pool.Pool
	StrictOptions  []rpc.StrictOption
	Recorder       observe.Recorder
	CollectTimeout time.Duration
	Corroboration  *rpc.CorroborationShared
	// Anchoring is nil for the strict-head strategy; the finalized preset
	// passes the strategy with its lag bound.
	Anchoring *AnchoringStrategy
}

// Bundle is the internal execution graph consumed by the root facade. It is
// intentionally data-only: callers still use the facade's public Client.
type Bundle struct {
	Policies   rpc.Registry
	Aggregator *aggregator.Aggregator
	Stage      *ReferenceStage
}

// Build assembles the method catalog, the reference stage, typed observation
// projectors, and the aggregator. Method policy binding lives here; the
// establishment algorithms remain owned by their respective modules.
func Build(in Inputs) (*Bundle, error) {
	if in.Pool == nil {
		return nil, fmt.Errorf("runtime: nil pool")
	}
	stage := NewReferenceStage(ReferenceStageConfig{
		Pool:              in.Pool,
		Timeout:           in.CollectTimeout,
		Recorder:          in.Recorder,
		FinalizedStrategy: in.Anchoring != nil && in.Anchoring.Finalized,
		MaxLag:            anchoringMaxLag(in.Anchoring),
	})
	anchorHash := stage.AnchorHashReader()
	anchorHeight := stage.AnchorHeightReader()
	// The registry is compiled from the frozen MethodSpec inventory: one
	// assembly for every registered method, with the equivalence tests
	// holding the compiled chains to the hand-written constructors. A method
	// without a spec cannot enter the registry, and a spec without a
	// registered catalog entry fails validation below.
	policies := rpc.Registry{}
	for _, spec := range methods.Specs() {
		policies[spec.Method] = methods.Compile(spec, methods.CompileDeps{
			AnchorHash:   anchorHash,
			AnchorHeight: anchorHeight,
		}, in.StrictOptions...)
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
			aggregator.WithReferenceStage(stage),
			aggregator.WithProjectors(projectors)),
		Stage: stage,
	}, nil
}

func anchoringMaxLag(strategy *AnchoringStrategy) uint64 {
	if strategy == nil {
		return 0
	}
	return strategy.MaxLag
}

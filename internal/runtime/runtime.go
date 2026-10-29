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
	Plans      map[string]rpc.ReadPlan
	Projectors map[string]rpc.ObservationProjector
	Aggregator *aggregator.Aggregator
	Stage      *ReferenceStage
}

// plannedReads are the read methods the default registry drives from
// explicit execution plans. The set grows one admission slice at a time as
// each method's plan equivalence lands; a method not listed here keeps its
// compiled policy chain until its slice is accepted. When the set covers
// the whole registered catalog, the default registry stops compiling
// decorator chains at all.
var plannedReads = map[string]bool{
	methods.MethodGetBalance: true,
	methods.MethodGetLogs:    true,
	methods.MethodCall:       true,
}

// Build assembles the method catalog, the reference stage, typed observation
// projectors, and the aggregator. Method binding lives here; the
// establishment algorithms remain owned by their respective modules. A
// planned method is compiled into its explicit execution plan and handed to
// the aggregator's plan pipeline; every other method is compiled into the
// decorator chain the legacy path drives.
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
		StrictOptions:     in.StrictOptions,
	})
	anchorHash := stage.AnchorHashReader()
	anchorHeight := stage.AnchorHeightReader()
	// The registry is assembled from the frozen MethodSpec inventory: every
	// registered method is driven either by its explicit execution plan or
	// by its compiled chain, and a method without either cannot enter. The
	// equivalence tests hold both shapes to the hand-written constructors.
	policies := rpc.Registry{}
	plans := make(map[string]rpc.ReadPlan, len(plannedReads))
	for _, spec := range methods.Specs() {
		if plannedReads[spec.Method] {
			plan, err := methods.CompilePlan(spec)
			if err != nil {
				return nil, fmt.Errorf("runtime: plan for %s: %w", spec.Method, err)
			}
			plans[spec.Method] = plan
			continue
		}
		policies[spec.Method] = methods.Compile(spec, methods.CompileDeps{
			AnchorHash:   anchorHash,
			AnchorHeight: anchorHeight,
		}, in.StrictOptions...)
	}
	if err := methods.ValidateReadCoverage(policies, plans); err != nil {
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
		Policies:   policies,
		Plans:      plans,
		Projectors: projectors,
		Aggregator: aggregator.New(in.Pool, policies,
			aggregator.WithRecorder(in.Recorder),
			aggregator.WithCollectTimeout(in.CollectTimeout),
			aggregator.WithCorroboration(in.Corroboration),
			aggregator.WithReferenceStage(stage),
			aggregator.WithPlans(plans),
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

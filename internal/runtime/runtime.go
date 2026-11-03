// Package runtime owns the production composition of the source pool with its
// transport dialers, the corroboration policy, the reference establishment
// stage, the RPC method catalog, typed observation projectors, and the
// aggregate execution pipeline.
//
// It deliberately does not expose a public client. The ethguard facade parses
// business options and hands them to Compose; method binding, the reference
// stage, and the anchor readers all live here, which makes the assembly
// visible in the single Composition Root: method policies receive
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
// Compose fills the pool, corroboration policy, and resolver; Build alone
// (tests, synthetic fleets) leaves them zero.
type Bundle struct {
	Pool          *pool.Pool
	Corroboration *rpc.CorroborationShared
	Resolver      ReferenceResolver
	Policies      rpc.Registry
	Plans         map[string]rpc.ReadPlan
	Projectors    map[string]rpc.ObservationProjector
	Aggregator    *aggregator.Aggregator
	Stage         *ReferenceStage
}

// Build assembles the method catalog, the reference stage, typed observation
// projectors, and the aggregator. Method binding lives here; the
// establishment algorithms remain owned by their respective modules. The
// default registry drives every registered read from its explicit execution
// plan and generates no decorator chains: read corroboration is governed by
// the invocation's frozen snapshot, and the strict options input stays with
// the establishment algorithms that own their own threshold formulas.
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
	// The registries are assembled from the frozen MethodSpec inventory: one
	// explicit plan per registered method, and the projector binding the
	// plans name. The coverage gate holds the catalog to its plans; a method
	// without a plan cannot fall through to transparent forwarding.
	policies := rpc.Registry{}
	plans, err := buildMethodRegistry()
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	if err := methods.ValidateReadCoverage(policies, plans); err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	projectors, err := buildProjectorRegistry()
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
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

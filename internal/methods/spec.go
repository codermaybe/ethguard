package methods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// ResolverKind names the resolution algorithm compiled for a method.
type ResolverKind = rpcengine.ResolverKind

const (
	ResolverStrict     = rpcengine.ResolverStrict
	ResolverMedian     = rpcengine.ResolverMedian
	ResolverLocal      = rpcengine.ResolverLocal
	ResolverCallRevert = rpcengine.ResolverCallRevert
)

// PendingMode declares a method's pending handling at one position.
type PendingMode = rpcengine.PendingMode

const (
	PendingNone    = rpcengine.PendingNone
	PendingObserve = rpcengine.PendingObserve
	PendingRefuse  = rpcengine.PendingRefuse
)

// PendingSpec is one method's pending declaration.
type PendingSpec = rpcengine.PendingSpec

// ReferenceShape names the request shape that can carry a moving tag.
type ReferenceShape = rpcengine.ReferenceShape

const (
	ReferenceShapeNone           = rpcengine.ReferenceShapeNone
	ReferenceShapeHashAt         = rpcengine.ReferenceShapeHashAt
	ReferenceShapeHeightAt       = rpcengine.ReferenceShapeHeightAt
	ReferenceShapeHeightFieldsAt = rpcengine.ReferenceShapeHeightFieldsAt
)

// ReferenceSpec declares where a request can ride a moving tag. Declaring is
// all a method does: the aggregator establishes the reference through the
// reference stage on the invocation's frozen basis, and the plan rewrites
// from the established evidence. A zero spec needs no reference.
// See docs/design/codebase-topology.md.
type ReferenceSpec = rpcengine.ReferenceSpec

// AdmissionFunc is the method-owned admission hook signature.
type AdmissionFunc = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error)

// Observation projector IDs. A spec declares the projection family its read
// belongs to by business identity, not by method name: two methods sharing
// one observation shape share one ID, and the runtime binds what each ID
// resolves to. An empty declaration keeps the read unprojected — it stays
// empty instead of falling back to the method name.
const (
	// ProjectorScalarStandard is the scalar projection shared by
	// `eth_getBalance` and `eth_call`.
	ProjectorScalarStandard = "scalar-standard"
	// ProjectorBlock is the block-object projection shared by both block
	// methods.
	ProjectorBlock = "block"
	// ProjectorReceipt is the receipt-object projection.
	ProjectorReceipt = "receipt"
	// ProjectorBlockReceipts is the block-receipt list projection.
	ProjectorBlockReceipts = "block-receipts"
	// ProjectorTransaction is the transaction-object projection.
	ProjectorTransaction = "transaction"
	// ProjectorLogs is the log list projection.
	ProjectorLogs = "logs"
	// ProjectorFeeHistory is the fee-history range projection.
	ProjectorFeeHistory = "fee-history"
	// ProjectorSyncing separates official progress from provider counters.
	ProjectorSyncing    = "syncing"
	ProjectorSimulateV1 = "simulate-v1"
)

// MethodSpec is the data form of one read method's contract: resolver, value
// domain, reference declaration, pending handling, and hooks. Compile turns
// it into the read plan the runtime installs; the field set is frozen —
// a new dimension goes through the call-revert escape hatch first, never
// through a new field for one method.
type MethodSpec struct {
	Method string

	Resolver ResolverKind

	// Normalize is the comparison-key function; Encode, when set, is the
	// canonical value encoder. KeySource is the frozen three-state
	// declaration (rpc.RuleKeyCanonicalBytes | rpc.RuleKeyNormalizer | ""):
	// canonical-bytes pairs with WithCanonicalValue, normalizer-with-encoder
	// pairs with WithCanonicalObject, and an empty declaration has no
	// encoder (raw results, local and median sampling).
	Normalize func(json.RawMessage) (string, error)
	Encode    func(json.RawMessage) (json.RawMessage, error)
	KeySource string

	CompareKind string

	Reference ReferenceSpec
	Pending   PendingSpec

	// Prepare runs before reference establishment; Admit runs after collection
	// with the same-source caller. The execution pipeline fixes their order.
	Prepare func(context.Context, rpc.Request) (rpc.Request, error)
	Admit   AdmissionFunc

	// ProcessCanonical selects the processed-only execution contract. It
	// requires Strict and canonical-bytes, excludes Admit/Encode and pending
	// local observation, and leaves Normalize independently available.
	ProcessCanonical rpcengine.CanonicalProcessor

	// ObservationProjectorID names the runtime-bound projector; the spec
	// never carries a factory (internal/methods stays clear of projection/typed).
	ObservationProjectorID string
}

// SpecFor returns the registered spec for one method name. It is the lookup
// behind PlanFor and the coverage gates.
func SpecFor(method string) (MethodSpec, bool) {
	for _, spec := range Specs() {
		if spec.Method == method {
			return spec, true
		}
	}
	return MethodSpec{}, false
}

// PlanFor compiles the registered spec for one method. Tools and offline
// verifiers use it to run exactly the resolver the registry installs.
func PlanFor(method string) (rpcengine.ReadPlan, error) {
	spec, ok := SpecFor(method)
	if !ok {
		return rpcengine.ReadPlan{}, fmt.Errorf("methods: %s is not a registered read method", method)
	}
	return CompilePlan(spec)
}

// CompileResolver assembles one spec's resolver with explicit strict options
// — the same assembly CompilePlan runs. The establishment algorithms keep
// their own threshold formulas by recorded decision, so they compile the
// head resolver directly instead of riding the plan's defaults.
func CompileResolver(spec MethodSpec, opts []rpcengine.StrictOption) (rpcengine.Resolver, error) {
	return assembleResolver(spec, opts)
}

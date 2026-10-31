package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
)

// ResolverKind names the resolver a compiled method assembles. It is the
// plan-level resolver class in rpc: the kind — not the number of decorators
// around it — decides every capability the composed policy exposes: only
// strict and call-revert corroborate, only median and local sample without
// clustering, and wrapping a non-corroborating resolver never acquires a
// corroboration claim.
type ResolverKind = rpc.ResolverKind

const (
	ResolverStrict     = rpc.ResolverStrict
	ResolverMedian     = rpc.ResolverMedian
	ResolverLocal      = rpc.ResolverLocal
	ResolverCallRevert = rpc.ResolverCallRevert
	// ResolverBlock is the strict resolver under the block wrapper: byHash
	// identity + header-only source spelling restoration.
	ResolverBlock = rpc.ResolverBlock
)

// PendingMode declares a method's pending handling at one position.
type PendingMode = rpc.PendingMode

const (
	PendingNone    = rpc.PendingNone
	PendingObserve = rpc.PendingObserve
	PendingRefuse  = rpc.PendingRefuse
)

// PendingSpec is one method's pending declaration.
type PendingSpec = rpc.PendingSpec

// ReferenceShape names the request shape that can carry a moving tag.
type ReferenceShape = rpc.ReferenceShape

const (
	ReferenceShapeNone           = rpc.ReferenceShapeNone
	ReferenceShapeHashAt         = rpc.ReferenceShapeHashAt
	ReferenceShapeHeightAt       = rpc.ReferenceShapeHeightAt
	ReferenceShapeHeightFieldsAt = rpc.ReferenceShapeHeightFieldsAt
)

// ReferenceSpec declares where a request can ride a moving tag. Declaring is
// all a method does: the aggregator establishes the reference through the
// reference stage on the invocation's frozen basis, and the anchor layers
// rewrite from the established evidence. A zero spec means the method never
// needs a reference. See docs/design/method-spec-reference-freeze.md.
type ReferenceSpec = rpc.ReferenceSpec

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
)

// MethodSpec is the data form of one read method's contract: resolver, value
// domain, reference declaration, pending handling, and hooks. Compile turns
// it into the one policy the registry installs; the field set is frozen —
// a new dimension goes through the call-revert escape hatch first, never
// through a new field for one method.
type MethodSpec struct {
	Method string

	Resolver ResolverKind
	// ByHash distinguishes the two block methods' admission and identity.
	ByHash bool

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

	// Validate runs before any anchor layer; Admit runs after collection
	// with the same-source caller. Both are v1 hooks: the order is fixed by
	// Compile, not by the method.
	Validate func(context.Context, rpc.Request) error
	Admit    AdmissionFunc

	// ObservationProjectorID names the runtime-bound projector; the spec
	// never carries a factory (rpc/methods stays clear of projection/typed).
	ObservationProjectorID string
}

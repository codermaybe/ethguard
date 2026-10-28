package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
)

// ResolverKind names the resolver a compiled method assembles. The kind —
// not the number of decorators around it — decides every capability the
// composed policy exposes: only strict and call-revert corroborate, only
// median and local sample without clustering, and wrapping a non-corroborating
// resolver never acquires a corroboration claim.
type ResolverKind string

const (
	ResolverStrict     ResolverKind = "strict"
	ResolverMedian     ResolverKind = "median"
	ResolverLocal      ResolverKind = "local"
	ResolverCallRevert ResolverKind = "call-revert"
	// ResolverBlock is the strict resolver under the block wrapper: byHash
	// identity + header-only source spelling restoration.
	ResolverBlock ResolverKind = "block"
)

// PendingMode declares a method's pending handling at one position.
type PendingMode string

const (
	PendingNone    PendingMode = ""
	PendingObserve PendingMode = "local-observation"
	PendingRefuse  PendingMode = "local-reject"
)

// PendingSpec is one method's pending declaration.
type PendingSpec struct {
	Mode PendingMode
	Pos  int
}

// ReferenceShape names the request shape that can carry a moving tag.
type ReferenceShape string

const (
	ReferenceShapeNone           ReferenceShape = ""
	ReferenceShapeHashAt         ReferenceShape = "hash-at"
	ReferenceShapeHeightAt       ReferenceShape = "height-at"
	ReferenceShapeHeightFieldsAt ReferenceShape = "height-fields-at"
)

// ReferenceSpec declares where a request can ride a moving tag. Declaring is
// all a method does: the aggregator establishes the reference through the
// reference stage on the invocation's frozen basis, and the anchor layers
// rewrite from the established evidence. A zero spec means the method never
// needs a reference. See docs/design/method-spec-reference-freeze.md.
type ReferenceSpec struct {
	Kind      ReferenceShape
	Positions []int
	ParamPos  int
	Fields    []string
	// EIP1898 marks the object form {blockNumber: tag} accepted at the
	// single anchored position, rewritten to {blockHash}.
	EIP1898 bool
}

// AdmissionFunc is the method-owned admission hook signature.
type AdmissionFunc = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error)

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

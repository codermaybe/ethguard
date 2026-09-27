package rpc

import (
	"context"
	"encoding/json"
)

// Policy prepares requests and resolves raw observations. The contract lives
// beside the transport model; concrete per-method implementations live in
// rpc/methods, one file per RPC method.
type Policy interface {
	Prepare(context.Context, Request) (Request, error)
	Normalize(json.RawMessage) (string, error)
	Resolve([]Observation) (Decision, error)
}

// AdmissionCaller performs a dependent request against the same upstream
// source that produced the primary response.  Method policies use it for
// content that the primary JSON-RPC result references by commitment (for
// example uncle headers referenced by an execution block).
type AdmissionCaller func(context.Context, string, []any) (json.RawMessage, error)

// AdmissionPolicy validates a response before it is allowed into a voting
// cluster.  The returned evidence is attached to the winning Decision and is
// available to typed decoders; it is deliberately opaque to the rpc package.
// An admission error is a malformed source answer, not a transport failure.
type AdmissionPolicy interface {
	Admit(context.Context, Request, json.RawMessage, AdmissionCaller) (json.RawMessage, error)
}

// ObservationProjector is an optional semantic-room capability. It turns a
// generic observation into source-bound extensions for the selected core
// value. Policies that do not implement it retain the existing decision
// shape.
type ObservationProjector interface {
	ProjectObservation(json.RawMessage, CommonObservation) (ExtensionSet, error)
}

// CorroboratingPolicy is the optional capability of a policy whose resolution
// is adjudicated against an invocation's fixed voting basis: the aggregator
// supplies the corroboration mode and support floor captured before any
// request was issued (decision 0025), overriding construction-time options.
// Implementing it also selects the fixed-basis collection mode in the
// aggregator: every voting member is represented in the observation set,
// including members whose request a scheduling gate skipped.
type CorroboratingPolicy interface {
	ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error)
}

// Registry maps JSON-RPC method names to policies. An empty or nil registry
// forwards every method transparently.
type Registry map[string]Policy

// PolicyFor returns a registered policy or transparent forwarding.
func (r Registry) PolicyFor(method string) Policy {
	if p := r[method]; p != nil {
		return p
	}
	return ForwardPolicy{}
}

// BlockRefProvider is an optional policy capability. It reports the block
// reference introduced while preparing a request, if any. The aggregator uses
// it to retain the security-relevant pin in Decision.BlockRef without making
// every policy carry anchoring state.
type BlockRefProvider interface {
	BlockRef(original, prepared Request) string
}

// RPCErrorPolicy is an optional capability for policies whose value domain
// includes JSON-RPC error bodies. Ordinary strict/local/median policies leave
// protocol errors out of normalization; forwarded policies return them as the
// authoritative single-source answer.
type RPCErrorPolicy interface {
	IncludeRPCError() bool
}

// SingleSourcePolicy requests ordered forwarding instead of symmetric fan-out.
// Prepare decorators preserve this execution requirement.
type SingleSourcePolicy interface{ SingleSource() bool }

// AgreementPolicy identifies deterministic answers that require corroboration
// at an ethclient-shaped boundary, where callers cannot inspect Selected.
type AgreementPolicy interface{ RequiresAgreement() bool }

// LocalObservationPolicy is an optional per-request capability for methods
// that serve one request shape — pending — as a node-local observation
// rather than cross-source corroboration (decision 0035). The aggregator
// consults it after Prepare: when IsLocalObservation reports true for the
// prepared request, resolution goes to ResolveLocal (first healthy source
// stands, ceiling Selected, never Corroborated) and the corroboration
// gate is skipped for that invocation. Every other request shape resolves
// through the ordinary Policy path untouched.
//
// This is deliberately per-method opt-in (each method constructor applies
// its own decorator), never a global pending switch: supported shapes,
// positions, and local-reject rules stay method-specific.
type LocalObservationPolicy interface {
	Policy
	// IsLocalObservation reports whether the prepared request must resolve
	// as a node-local observation.
	IsLocalObservation(Request) bool
	// ResolveLocal resolves observations as a local observation: the first
	// source with a normalizable answer stands with Selected status and
	// its source as Support; no usable source fails Unknown.
	ResolveLocal([]Observation) (Decision, error)
}

// MethodRule is one layer of the method-rule index. Zero strings mean this
// layer does not declare that column. A decorator fills only its own fields
// and adopts the rest from the inner layer through WithInner. A resolver
// fills Resolver and CompareKind and is the end of the chain.
//
// The index is a read-only projection. Nothing in request handling reads it.
// Allowed values, and no finer classification:
//
//	Anchor:      "hash@<positions>", "height@<positions>",
//	             "height@<pos>.<field>,<field>", empty renders "none"
//	BlockRef:    "eip1898", empty renders "none"
//	Pending:     "local-observation", "local-reject", empty renders "none"
//	Resolver:    "strict", "call-revert", "median", "local"
//	CompareKind: "quantity", "object", "method-specific"
//	Result:      "canonical", "raw"
//
// Pending is the layer's own pending semantics, not the presence of a
// decorator. local-observation is a node-local observation capped at
// Selected. local-reject fails locally, contacts no upstream, and does not
// touch the anchor. none means that position has no pending form.
//
// CompareKind stays a coarse class. eth_call revert-data states and any
// other comparison-key internals do not belong in the index.
//
// Result records whether this method writes a canonical scalar. It is
// "canonical" or "raw". The index does not explain fields and does not
// generate tests. Request handling does not read it.
type MethodRule struct {
	Anchor      string
	BlockRef    string
	Pending     string
	Resolver    string
	CompareKind string
	Result      string
}

// WithInner keeps this layer's declared fields and fills the gaps from inner.
func (rule MethodRule) WithInner(inner MethodRule) MethodRule {
	return MethodRule{
		Anchor:      pickMethodRule(rule.Anchor, inner.Anchor),
		BlockRef:    pickMethodRule(rule.BlockRef, inner.BlockRef),
		Pending:     pickMethodRule(rule.Pending, inner.Pending),
		Resolver:    pickMethodRule(rule.Resolver, inner.Resolver),
		CompareKind: pickMethodRule(rule.CompareKind, inner.CompareKind),
		Result:      pickMethodRule(rule.Result, inner.Result),
	}
}

func pickMethodRule(own, inner string) string {
	if own != "" {
		return own
	}
	return inner
}

// MethodRuleSource is an optional introspection capability. It reports this
// layer's rule composition, including the inner layer, and the next policy
// in the decorator chain. A nil next policy is the resolver.
//
// It is not part of Policy. Prepare, Normalize, and Resolve do not consult
// it. ForwardPolicy, SelectedPolicy, and FinalizedHead are not registered
// method rules and do not implement it. The methods test fails, naming the
// method and the concrete type, when a visited layer is missing. A walk of
// registered methods must not grow an allow-undeclared exception.
type MethodRuleSource interface {
	MethodRule() (MethodRule, Policy)
}

const (
	RulePendingLocalObservation = "local-observation"
	RulePendingLocalReject      = "local-reject"
	RuleBlockRefEIP1898         = "eip1898"
	RuleResolverStrict          = "strict"
	RuleResolverCallRevert      = "call-revert"
	RuleResolverMedian          = "median"
	RuleResolverLocal           = "local"
	RuleCompareQuantity         = "quantity"
	RuleCompareObject           = "object"
	RuleCompareMethodSpecific   = "method-specific"
	RuleResultCanonical         = "canonical"
	RuleResultRaw               = "raw"
)

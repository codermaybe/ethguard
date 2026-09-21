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

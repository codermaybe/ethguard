// Package rpc contains the small transport and decision model shared by the
// pool, method policies, and aggregator.
package rpc

import (
	"encoding/json"
	"errors"
	"time"
)

// Request is an arbitrary JSON-RPC request.
type Request struct {
	Method string
	Params []any
}

// SourceRef is immutable source identity carried in observations and decision
// evidence. Keeping this value type in rpc prevents the policy layer from
// depending on pool's connection and health implementation.
// Strict resolution requires a nonempty, unique ID for every observation.
type SourceRef struct {
	Name string
	URL  string
	ID   string
}

// RPCError is the structured protocol error returned by a JSON-RPC upstream.
// Data is retained verbatim because contract-specific callers may need it.
type RPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

// Raw returns a stable JSON representation of the protocol error body. It is
// used only by policies that explicitly opt into treating protocol errors as
// clusterable values.
func (e RPCError) Raw() json.RawMessage {
	body := struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	}{Code: e.Code, Message: e.Message, Data: e.Data}
	raw, _ := json.Marshal(body)
	return raw
}

// Observation is one source's raw response to a request.
type Observation struct {
	Source   SourceRef
	Value    json.RawMessage
	Err      error
	RPCError *RPCError
	// Evidence is method-specific admission evidence (for example, the
	// same-source uncle headers needed to complete a block body).
	Evidence json.RawMessage
}

// Status describes the confidence class of a successful decision.
// Unknown is the zero value and carries no claim: it must appear only with
// a non-nil error (or an uninitialized value). Successful results are always
// Corroborated, Selected, or Forwarded, set explicitly by the resolving
// policy. Failed is never a Status: failures are carried by error returns
// and the observability event's "failed" string. That string is
// observe.DecisionEvent.Status, not a value of this enum: the event is
// "failed" when the call returns an error, and Status.String() otherwise.
// A failed Decision.Status is Unknown.
// Corroborated is provider-backed cross-source evidence: independent
// sources agreeing under the declared support rule. It is not Ethereum
// consensus, a canonical proof, cryptographic finality, or light-client
// verification.
type Status int

const (
	Unknown Status = iota
	Corroborated
	Selected
	Forwarded
)

// ReferenceEvidence describes the prerequisite used for a moving-state read.
// Reported finality is provider evidence, not an Ethereum consensus proof.
// Status follows the same contract as Decision: Unknown with an error,
// otherwise Corroborated, Selected, or Forwarded set explicitly.
//
// Freshness is this call only (decision 0026). The reference is established
// inside the invocation that needs it and held only for that invocation's
// dependent reads. There is no cross-call reuse and no TTL: a cache would
// present another call's head as this call's evidence. ObservedAt is the
// establishment time for this call, including when establishment fails.
//
// Failure is a Prepare-phase error wrapping the anchor cause. The business
// method is not fanned out. Status stays Unknown, and Faults recorded by
// the failed establishment stay on this evidence.
type ReferenceEvidence struct {
	Tag          string
	Hash         string
	Height       uint64
	Status       Status
	Origin       string
	ObservedAt   time.Time
	Participants []SourceRef
	Support      []SourceRef
	Reject       []SourceRef
	Faults       []error
}

// Clone isolates evidence held by a cache from callers and recorders.
func (r ReferenceEvidence) Clone() ReferenceEvidence {
	r.Participants = append([]SourceRef(nil), r.Participants...)
	r.Support = append([]SourceRef(nil), r.Support...)
	r.Reject = append([]SourceRef(nil), r.Reject...)
	r.Faults = append([]error(nil), r.Faults...)
	return r
}

// Decision is the minimal result returned by aggregation. Support and Reject
// provide enough evidence for diagnostics without introducing an evidence
// tree. Faults records collection-phase failures that did not prevent the
// decision, so partial fan-outs stay diagnosable on success paths too.
type Decision struct {
	Reference    *ReferenceEvidence
	Participants []SourceRef
	Status       Status
	// Value is the payload returned for this decision. Migrated quantities
	// and successful DATA write the canonical encoding, which does not follow
	// the representative source's spelling. eth_feeHistory writes the
	// canonical object of its protected fields; its comparison key stays the
	// feeHistory key. eth_getLogs writes the canonical array of the fields its
	// mask retains; its comparison key stays the existing log key.
	// eth_createAccessList writes the canonical object of the fields its mask
	// retains; its comparison key stays the existing access-list key. Other
	// generic Strict decisions copy one admitted source value from the winning
	// cluster and do not change its fields. A
	// successful eth_call value is canonical DATA. An eth_call revert may
	// apply a reductive projection (decision 0039 rule 4e, option (i)): it
	// may delete a field that lacks corroboration, and it may not add,
	// replace, or splice fields from another source. Revert payloads, and
	// methods that have not migrated, keep that source value.
	Value json.RawMessage
	// Observed is the optional generic observation-room result for this
	// invocation. It contains source-backed common fields and residuals; it
	// carries no Ethereum semantic claim. Method packages may project it into
	// their compile-time standard field set.
	Observed *CommonObservation
	// Extensions contains non-core fields projected by an optional method
	// semantic projector and bound to Value's core fingerprint.
	Extensions *ExtensionSet
	BlockRef   string
	Support    []SourceRef
	Reject     []SourceRef
	Faults     []error
	// Evidence belongs to the selected value and is populated by an
	// AdmissionPolicy. It is opaque to generic callers. eth_call also stores
	// withheld revert-data bytes here when rule 4e removes them from Value;
	// those bytes are audit material, not a corroborated field.
	Evidence json.RawMessage
	// Representative is the first supporter of the winning cluster in this
	// call's captured member order. For a method that has not migrated, Value
	// is that source's admitted payload. For a migrated quantity, successful
	// DATA result, eth_feeHistory, eth_getLogs, or eth_createAccessList, Value
	// is the canonical encoding and this field only records who was first. It
	// is set wherever that cluster is
	// carried out, including decisions that fail with the Value attached. It
	// stays zero when there is no valid response, and on Local, Forward, and
	// ResolveLocal. It carries no ordinal; the ordinal is the source's
	// position in the captured order, which the caller already has.
	Representative SourceRef
	// Dimensions is method-specific evidence. Generic Strict leaves it nil.
	// eth_call records execution and revert-data Support, Reject, and Abstain.
	Dimensions []DimensionEvidence
	// DeletedFields lists top-level fields a reductive projection removed
	// from Value. Nil means this resolver did not delete anything.
	DeletedFields []string
}

// DimensionEvidence is one method-declared dimension of a decision.
// Returned is false when the dimension was withheld. Abstain is sources
// that asserted the parent outcome but not this dimension; they are not
// Reject.
type DimensionEvidence struct {
	Name     string
	Support  []SourceRef
	Reject   []SourceRef
	Abstain  []SourceRef
	Returned bool
}

// IsCorroborated reports whether d is a successful corroborated result.
// The error is part of the result contract: Unknown is the zero value of
// Status, so a failed call must never be interpreted from Status alone.
// It remains a defensive convenience predicate, not the validity model
// itself: error paths must return Status Unknown, success paths must set
// Corroborated, Selected, or Forwarded explicitly. Decision 0038 keeps
// this predicate and does not add a general validity helper. Corroboration
// gates still write the err == nil split inline so an existing error is
// not rewritten into a conflict.
func (d Decision) IsCorroborated(err error) bool {
	return err == nil && d.Status == Corroborated
}

// ObservedResult is the explicit dynamic result shape for callers handling
// methods without a semantic projection. It must not be decoded as a Geth
// core type or treated as Corroborated standard data.
type ObservedResult struct {
	Value       json.RawMessage
	Status      Status
	Observation CommonObservation
}

// ObservedView exposes a decision as an observation-only result when the
// aggregator collected a CommonSet. Forwarded calls or failed collections
// return false when no observation set is available.
func (d Decision) ObservedView() (ObservedResult, bool) {
	if d.Observed == nil {
		return ObservedResult{}, false
	}
	return ObservedResult{
		Value:       append(json.RawMessage(nil), d.Value...),
		Status:      d.Status,
		Observation: *d.Observed,
	}, true
}

// String names the confidence class for observability sinks.
func (s Status) String() string {
	switch s {
	case Unknown:
		return "unknown"
	case Corroborated:
		return "corroborated"
	case Selected:
		return "selected"
	case Forwarded:
		return "forwarded"
	default:
		return "unknown"
	}
}

// Error classes for the decision pipeline.
var (
	ErrConflict          = errors.New("rpc: conflicting responses")
	ErrNoValidResponse   = errors.New("rpc: no valid response")
	ErrMalformedResponse = errors.New("rpc: malformed response")
	ErrInvalidRequest    = errors.New("rpc: invalid request")
)

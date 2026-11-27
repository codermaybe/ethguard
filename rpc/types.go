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

// SourceFault attributes one collection-phase error to the source that
// produced it. It is diagnostic evidence, not an adjudication input: Faults
// stays the human-readable list, while SourceFaults lets report generators
// attribute failures without parsing error strings.
type SourceFault struct {
	Source SourceRef
	Err    error
}

// RPCError is the structured protocol error returned by a JSON-RPC upstream.
// Data is retained verbatim because contract-specific callers may need it.
type RPCError struct {
	Code         int
	Message      string
	Data         json.RawMessage
	DataPresence ErrorDataPresenceState
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
	// Attempted is scheduling evidence, independent of wire progress.
	Attempted bool
	Source    SourceRef
	Response  Response
	// Evidence remains same-source admission evidence, never a second raw answer.
	Evidence json.RawMessage
}

// Status describes the confidence class a decision's aggregation reached.
// Unknown is the zero value and carries no claim: the invocation formed no
// adjudicated value (local rejection before any verdict, a failed
// establishment or collection, a policy resolution failure), and such a
// decision always travels with a non-nil error on the call. Corroborated,
// Selected, and Forwarded are set explicitly by the resolving policy. A
// failed call may still carry a non-Unknown Status: when the aggregation
// itself succeeded and the business failure happened after it — a typed
// binding failure, a corroborated receipt-null answered as NotFound, a
// corroborated revert answered as RevertError — the decision keeps the
// facts it formed and the error carries the business conclusion. No layer
// rewrites a formed Status to Unknown just because an error exists.
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
// Status follows Decision: a formed status survives a later guarantee failure.
//
// Freshness is this call only (decision 0026). The reference is established
// inside the invocation that needs it and held only for that invocation's
// dependent reads. There is no cross-call reuse and no TTL: a cache would
// present another call's head as this call's evidence. ObservedAt is the
// establishment time for this call, including when establishment fails.
//
// Failure is a Prepare-phase error wrapping the anchor cause. The business
// method is not fanned out. Any formed Status and the failed establishment
// evidence remain available; Reason records an unmet guarantee separately.
// Before final resolution, Adjudication.Candidates may record decoded head
// groups compared by reference selection. Sources then remain Admitted and
// unassessed as votes; a fork at that height prevents final resolution.
type ReferenceEvidence struct {
	Sources      []SourceSummary
	Adjudication Adjudication
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
	// SourceFaults is the structured attribution of Faults that belong to a
	// single source. CollectionFaults holds pool-level failures that cannot
	// be attributed to one source.
	SourceFaults     []SourceFault
	CollectionFaults []error
}

// Decision carries the selected result and its adjudication. Sources exposes
// communication, protocol and method facts without copying every raw payload.
// Support/Reject and fault views derive from the same resolver judgments;
// collection and internal diagnostics remain independent of source blame.
type Decision struct {
	Sources      []SourceSummary
	Adjudication Adjudication
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
	// retains; its comparison key stays the existing access-list key.
	// eth_getTransactionReceipt writes the canonical object of its protected
	// receipt fields, including the official from field when present. Its
	// comparison key is the same canonical object. eth_getBlockReceipts uses the
	// same protected receipt model in a canonical array. Other
	// generic Strict decisions copy one admitted source value from the winning
	// cluster and do not change its fields. A
	// successful eth_call value is canonical DATA. An eth_call revert may
	// apply a reductive projection (decision 0039 rule 4e, option (i)): it
	// may delete a field that lacks corroboration, and it may not add,
	// replace, or splice fields from another source. Revert code/message are
	// carried in RevertContext; the protected Value keeps only accepted data.
	// Methods that have not migrated keep their source value.
	Value json.RawMessage
	// Revert carries provider error context for a corroborated eth_call
	// revert. Code and Message are diagnostics, not protected result fields;
	// the canonical Value keeps only the accepted data dimension.
	Revert *RevertContext
	// Extensions contains accepted non-core fields projected by an optional
	// method semantic projector and bound to Value's core fingerprint.
	Extensions *ExtensionSet
	// Diagnostics contains source-backed observations that did not enter the
	// accepted field set. It carries no Ethereum semantic claim and does not
	// alter the core decision.
	Diagnostics *FieldDiagnostics
	BlockRef    string
	Support     []SourceRef
	Reject      []SourceRef
	Faults      []error
	// Prepared is the frozen request fact observed at the policy boundary
	// after moving-tag rewriting: the method and the parameter JSON that
	// actually went out, owned independently of the caller's objects. It is
	// nil when Prepare failed before producing a request. Diagnostic callers
	// must record this value rather than reconstructing it.
	Prepared *PreparedRequest
	// SourceFaults attributes each Fault that belongs to one source;
	// CollectionFaults holds pool-level failures. Faults is the human-readable
	// attempted-source, collection and internal diagnostic view.
	SourceFaults     []SourceFault
	CollectionFaults []error
	// Provenance belongs to the selected value and is populated by an
	// the plan admission hook. It is opaque to generic callers. eth_call also stores
	// withheld revert-data bytes here when rule 4e removes them from Value;
	// those bytes are audit material, not a corroborated field.
	Provenance json.RawMessage
	// Representative is the first supporter of the winning cluster in this
	// call's captured member order. For a method that has not migrated, Value
	// is that source's admitted payload. For a migrated quantity, successful
	// DATA result, eth_feeHistory, eth_getLogs, eth_createAccessList, or
	// eth_getTransactionReceipt, or eth_getBlockReceipts, Value
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

// RevertContext is the source-selected context of an eth_call execution
// revert. It is kept separate from Decision.Value because JSON-RPC error code
// and message are provider dialect, not execution-apis result fields.
type RevertContext struct {
	Code    int
	Message string
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
	ErrInsufficientSupport = errors.New("rpc: insufficient support")
	ErrGuaranteeNotMet     = errors.New("rpc: required guarantee not met")
	ErrHashMismatch        = errors.New("rpc: transaction hash does not match submitted payload")
	ErrConflict            = errors.New("rpc: conflicting responses")
	ErrNoValidResponse     = errors.New("rpc: no valid response")
	ErrMalformedResponse   = errors.New("rpc: malformed response")
	ErrInvalidRequest      = errors.New("rpc: invalid request")
)

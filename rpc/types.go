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
type Status int

const (
	Verified Status = iota
	Selected
	Forwarded
)

// ReferenceEvidence describes the prerequisite used for a moving-state read.
// Reported finality is provider evidence, not a consensus proof.
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
	Value        json.RawMessage
	BlockRef     string
	Support      []SourceRef
	Reject       []SourceRef
	Faults       []error
	// Evidence belongs to the selected value and is populated by an
	// AdmissionPolicy. It is opaque to generic callers.
	Evidence json.RawMessage
}

// String names the confidence class for observability sinks.
func (s Status) String() string {
	switch s {
	case Verified:
		return "verified"
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

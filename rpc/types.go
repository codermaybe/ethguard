// Package rpc contains the small transport and decision model shared by the
// pool, method policies, and aggregator.
package rpc

import (
	"encoding/json"
	"errors"

	"ethguard/pool"
)

// Request is an arbitrary JSON-RPC request.
type Request struct {
	Method string
	Params []any
}

// Observation is one source's raw response to a request.
type Observation struct {
	Source *pool.Source
	Value  json.RawMessage
	Err    error
}

// Status describes the confidence class of a successful decision.
type Status int

const (
	Verified Status = iota
	Selected
	Forwarded
)

// Decision is the minimal result returned by aggregation. Support and Reject
// provide enough evidence for diagnostics without introducing an evidence
// tree. Faults records collection-phase failures that did not prevent the
// decision, so partial fan-outs stay diagnosable on success paths too.
type Decision struct {
	Status  Status
	Value   json.RawMessage
	Support []*pool.Source
	Reject  []*pool.Source
	Faults  []error
}

// String names the confidence class for observability sinks. The values are
// part of the observability contract consumed by metrics and logs; they must
// stay in sync with the status labels documented on observe.DecisionEvent.
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

// Error classes for the decision pipeline. Every failure wraps exactly one
// class; match with errors.Is. These sentinels are part of the public
// stability contract (decision 0009): new classes may appear, existing ones
// will not be removed or merged.
var (
	// ErrConflict reports that sources were verified but disagreed, or that
	// a configured quorum was unmet. Decision evidence names the clusters.
	ErrConflict = errors.New("rpc: conflicting responses")

	// ErrNoValidResponse reports that sources answered but no response
	// survived normalization — including fleets whose every emission was a
	// JSON-RPC error object.
	ErrNoValidResponse = errors.New("rpc: no valid response")

	// ErrMalformedResponse reports a decode-stage failure at a typed
	// boundary: a resolved value cannot be shaped into its target.
	ErrMalformedResponse = errors.New("rpc: malformed response")
)

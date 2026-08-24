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

var ErrConflict = errors.New("rpc: conflicting responses")

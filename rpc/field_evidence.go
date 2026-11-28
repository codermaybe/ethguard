package rpc

import "encoding/json"

// FieldObservation is one JSON path/value pair observed from one or more
// upstreams. Value is copied and remains valid after the input observations
// are released.
type FieldObservation struct {
	Path    string
	Value   json.RawMessage
	Sources []SourceRef
}

// ConflictField retains all values observed for a path when no single value
// can be selected as the common value. It is intentionally semantic-free: the
// rpc package does not decide whether a field is standard or Ethereum-specific.
type ConflictField struct {
	Path   string
	Values []FieldObservation
}

// FieldDiagnostics contains source-backed observations that were not eligible
// for the accepted field set. It is an audit view, not a protocol verdict.
type FieldDiagnostics struct {
	Residuals []FieldObservation
	Conflicts []ConflictField
}

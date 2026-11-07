package rpc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PreparedRequest is the request fact recorded on a Decision: the method and
// the parameter JSON that actually went out. It is a frozen snapshot, not a
// live view of the caller's objects — the bytes are owned by the decision and
// shared with nobody.
type PreparedRequest struct {
	Method string
	// Params is the prepared parameter JSON array, including any
	// reference rewrite the prepare phase introduced. A request prepared
	// without parameters keeps the zero value: absence of the params member
	// on the wire and absence here are the same fact. The bytes are
	// independently owned; consumers must not alias them.
	Params json.RawMessage
}

// Clone returns an independent copy of the prepared request.
func (p *PreparedRequest) Clone() *PreparedRequest {
	if p == nil {
		return nil
	}
	out := &PreparedRequest{Method: p.Method}
	if p.Params != nil {
		out.Params = append(json.RawMessage(nil), p.Params...)
	}
	return out
}

// FreezeParams performs the one controlled serialization of an executable
// request's parameters. Each element is marshaled exactly once — custom
// MarshalJSON implementations run here and nowhere else — and the frozen
// bytes travel both to the wire and into the prepared-request record, so a
// nondeterministic marshaler cannot produce two different facts.
//
// It returns the executable parameter list (elements frozen as raw JSON) and
// the joined array snapshot for the record. Nil parameters stay nil in both
// results, matching a wire request without a params member. A serialization
// failure is an argument error: the caller fails the operation before any
// fan-out and must not fall back to the unfrozen values.
func FreezeParams(params []any) ([]any, json.RawMessage, error) {
	if params == nil {
		return nil, nil, nil
	}
	frozen := make([]any, len(params))
	parts := make([]string, len(params))
	for i, element := range params {
		raw, err := json.Marshal(element)
		if err != nil {
			return nil, nil, fmt.Errorf("rpc: encode parameter %d: %v: %w", i, err, ErrInvalidRequest)
		}
		frozen[i] = json.RawMessage(raw)
		parts[i] = string(raw)
	}
	snapshot := json.RawMessage("[" + strings.Join(parts, ",") + "]")
	return frozen, snapshot, nil
}

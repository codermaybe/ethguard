package rpc

import "encoding/json"

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

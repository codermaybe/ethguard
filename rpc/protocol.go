package rpc

import "encoding/json"

// ProtocolError is the smallest protocol-error capability: an error that
// carries a JSON-RPC-style numeric code. Pool health, write adjudication and
// fault classification consume only this interface, so replacing the
// connection Adapter never teaches the upper layers a third-party error type
// (transport seam Slice 1c).
//
// The interface is structurally identical to the go-ethereum rpc.Error that
// the Geth compatibility Adapter surfaces, so decoded provider errors cross
// the transport seam without adapter-specific translation; custom
// HTTP/WSS Adapters implement it directly.
type ProtocolError interface {
	error
	ErrorCode() int
}

// DataError is the decoded form of a protocol error's data field. The value
// is whatever the Adapter's JSON codec produced; it has already lost the
// distinction between an absent field and a JSON null, which is why
// presence-preserving Adapters should also implement ErrorDataPresence.
//
// It is structurally identical to the go-ethereum rpc.DataError satisfied by
// the Geth compatibility Adapter's decoded errors.
type DataError interface {
	error
	ErrorData() any
}

// ErrorDataPresence is the raw-JSON form of a protocol error's data field.
// present is false when the field was absent on the wire and true for a JSON
// null or any other value; a present null is the raw bytes "null". Adapters
// that preserve the response envelope offer this capability; the plain Geth
// WSS Adapter cannot, and consumers must keep treating it as unknown
// presence (decisions 0056/0060: presence is not raised to RPCError in the
// transport slices).
type ErrorDataPresence interface {
	error
	ErrorDataJSON() (json.RawMessage, bool)
}

// RateLimitError marks an error as explicit provider throttling (for example
// HTTP 429), which differs from being unreachable. Health accounting and the
// adaptive scheduler back off faster on these errors.
type RateLimitError interface {
	error
	IsRateLimit() bool
}

// HTTPStatusError exposes the HTTP status of a failed HTTP exchange. It is
// provided by the transport Adapter's error wrapper; JSON-RPC-over-WSS
// errors never implement it.
type HTTPStatusError interface {
	error
	HTTPStatusCode() int
}

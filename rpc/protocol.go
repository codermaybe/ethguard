package rpc

import "encoding/json"

// ProtocolError is an error carrying a JSON-RPC-style numeric code. Pool
// health, write adjudication and fault classification consume this capability
// without depending on a transport implementation or third-party error type.
// Native HTTP and WSS errors implement it through the shared wire codec.
type ProtocolError interface {
	error
	ErrorCode() int
}

// DataError is the decoded form of a protocol error's data field. The value
// is whatever the adapter's JSON codec produced; it has already lost the
// distinction between an absent field and a JSON null, which is why
// presence-preserving adapters also implement ErrorDataPresence. Decoded
// errors from custom transports or test oracles can supply this view alone.
type DataError interface {
	error
	ErrorData() any
}

// ErrorDataPresence is the raw-JSON form of a protocol error's data field.
// present is false when the field was absent on the wire and true for a JSON
// null or any other value; a present null is the raw bytes "null". Both native
// adapters preserve this distinction through the shared wire codec.
// ProtocolAnswer records it in RPCError.DataPresence. An error that exposes
// only a nil decoded DataError leaves presence unknown.
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

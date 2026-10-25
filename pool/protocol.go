package pool

// protocolError is the smallest capability needed by pool health and
// scheduling: a JSON-RPC-like error code. Concrete protocol error types stay
// inside their connection Adapter.
type protocolError interface {
	error
	ErrorCode() int
}

type rateLimitMarker interface {
	IsRateLimit() bool
}

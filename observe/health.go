package observe

import "time"

// HealthSnapshot is a point-in-time copy of source health observations.
type HealthSnapshot struct {
	Successes            uint64
	Failures             uint64
	ConsecutiveFailures  uint64
	RateLimited          uint64
	ProtocolServerErrors uint64
	LatencyEWMA          time.Duration
	// LatencyP95 and LatencyP99 are nearest-rank percentiles over the recent
	// sample window; while fewer samples than the rank requires exist they
	// degrade to the window's largest available rank estimate, and they are
	// zero only before the first sample.
	LatencyP95 time.Duration
	LatencyP99 time.Duration
	// RequestPace is the current per-source inter-request target selected by
	// the adaptive scheduler. The in-flight fields expose its local bound.
	RequestPace          time.Duration
	RequestInFlight      int
	RequestInFlightLimit int
	LastSuccess          time.Time
	LastFailure          time.Time
	LastError            error
	TipHeight            uint64
	TipObservedAt        time.Time
}

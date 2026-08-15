package pool

import (
	"sync"
	"time"
)

// monitor holds lightweight health and latency statistics for one Source.
// All methods are safe for concurrent use.
type monitor struct {
	mu sync.Mutex

	successes uint64
	failures  uint64
	// consecutiveFailures resets on success.
	consecutiveFailures uint64

	// latencyEWMA is a simple exponential moving average of successful RTTs.
	latencyEWMA time.Duration
	lastSuccess time.Time
	lastError   error
	lastFailure time.Time
}

// HealthSnapshot is a point-in-time copy of source health observations.
type HealthSnapshot struct {
	Successes           uint64
	Failures            uint64
	ConsecutiveFailures uint64
	LatencyEWMA         time.Duration
	LastSuccess         time.Time
	LastFailure         time.Time
	LastError           error
}

// eligibleFailureThreshold is deliberately small: repeated transport
// failures should stop consuming request capacity, while still allowing a
// later probe to recover the source.
const eligibleFailureThreshold = 3

const eligibleFailureCooldown = 30 * time.Second

func (m *monitor) observeSuccess(rtt time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.successes++
	m.consecutiveFailures = 0
	m.lastSuccess = time.Now()
	if m.latencyEWMA == 0 {
		m.latencyEWMA = rtt
	} else {
		// alpha ≈ 0.2
		m.latencyEWMA = (m.latencyEWMA*4 + rtt) / 5
	}
}

func (m *monitor) observeFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failures++
	m.consecutiveFailures++
	m.lastFailure = time.Now()
	m.lastError = err
}

func (m *monitor) snapshot() HealthSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	return HealthSnapshot{
		Successes:           m.successes,
		Failures:            m.failures,
		ConsecutiveFailures: m.consecutiveFailures,
		LatencyEWMA:         m.latencyEWMA,
		LastSuccess:         m.lastSuccess,
		LastFailure:         m.lastFailure,
		LastError:           m.lastError,
	}
}

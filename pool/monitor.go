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

	// tip is the latest block height observed by the tip sampler;
	// tipObservedAt is zero until the first sample arrives.
	tipHeight     uint64
	tipObservedAt time.Time
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
	// TipHeight is the latest block height observed by the tip sampler;
	// TipObservedAt is zero when sampling never reached this source.
	TipHeight     uint64
	TipObservedAt time.Time
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

// observeTip records one block-height sample from the tip sampler.
func (m *monitor) observeTip(height uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tipHeight = height
	m.tipObservedAt = time.Now()
}

// sampledTip returns the last observed tip; ok is false before any sample.
func (m *monitor) sampledTip() (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tipObservedAt.IsZero() {
		return 0, false
	}
	return m.tipHeight, true
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
		TipHeight:           m.tipHeight,
		TipObservedAt:       m.tipObservedAt,
	}
}

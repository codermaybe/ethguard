package pool

import (
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

// latencyWindow is the number of recent successful RTTs kept for percentile
// estimates. Count-based rather than time-based: predictable memory, and a
// quiet source keeps its last known distribution instead of decaying into
// ignorance.
const latencyWindow = 128

// monitor holds lightweight health and latency statistics for one Source.
// All methods are safe for concurrent use.
type monitor struct {
	mu sync.Mutex

	successes uint64
	failures  uint64
	// consecutiveFailures resets on success.
	consecutiveFailures uint64
	// rateLimited counts transport failures that were HTTP 429 responses:
	// the provider is alive but throttling us, which is operationally
	// different from being broken or unreachable.
	rateLimited uint64
	// protocolServerErrors counts JSON-RPC internal/server failures separately
	// from transport failures and rate limits.
	protocolServerErrors uint64

	// latencyEWMA is a simple exponential moving average of successful RTTs.
	latencyEWMA time.Duration
	lastSuccess time.Time
	lastError   error
	lastFailure time.Time

	// rtts is a ring of the most recent successful round-trip times, the raw
	// material for P95/P99 estimates.
	rtts  [latencyWindow]time.Duration
	rttN  uint64 // total samples ever pushed
	rttAt int    // next write index

	// tip is the latest block height observed by the tip sampler;
	// tipObservedAt is zero until the first sample arrives.
	tipHeight     uint64
	tipObservedAt time.Time
}

// HealthSnapshot is a point-in-time copy of source health observations.
type HealthSnapshot struct {
	Successes            uint64
	Failures             uint64
	ConsecutiveFailures  uint64
	RateLimited          uint64
	ProtocolServerErrors uint64
	LatencyEWMA          time.Duration
	// LatencyP95 and LatencyP99 are nearest-rank percentiles over the recent
	// sample window; zero while fewer samples than the rank exist.
	LatencyP95    time.Duration
	LatencyP99    time.Duration
	LastSuccess   time.Time
	LastFailure   time.Time
	LastError     error
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
	m.rtts[m.rttAt] = rtt
	m.rttAt = (m.rttAt + 1) % latencyWindow
	m.rttN++
}

func (m *monitor) observeFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failures++
	m.consecutiveFailures++
	var httpErr rpc.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 429 {
		m.rateLimited++
	}
	if isProtocolServerError(err) {
		m.protocolServerErrors++
	}
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

// circuitState reports the two fields the eligibility gate reads, without
// copying the latency window or computing percentiles: eligibility runs per
// source per request, so its read path must stay cheap.
func (m *monitor) circuitState() (consecutiveFailures uint64, lastFailure time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.consecutiveFailures, m.lastFailure
}

func (m *monitor) snapshot() HealthSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := HealthSnapshot{
		Successes:            m.successes,
		Failures:             m.failures,
		ConsecutiveFailures:  m.consecutiveFailures,
		RateLimited:          m.rateLimited,
		ProtocolServerErrors: m.protocolServerErrors,
		LatencyEWMA:          m.latencyEWMA,
		LastSuccess:          m.lastSuccess,
		LastFailure:          m.lastFailure,
		LastError:            m.lastError,
		TipHeight:            m.tipHeight,
		TipObservedAt:        m.tipObservedAt,
	}
	p95, p99 := m.percentilesLocked()
	snap.LatencyP95, snap.LatencyP99 = p95, p99
	return snap
}

// percentilesLocked computes nearest-rank P95/P99 over the live window.
// Caller holds m.mu.
func (m *monitor) percentilesLocked() (time.Duration, time.Duration) {
	n := int(m.rttN)
	if n > latencyWindow {
		n = latencyWindow
	}
	if n == 0 {
		return 0, 0
	}
	samples := make([]time.Duration, n)
	copy(samples, m.rtts[:])
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[nearestRank(0.95, n)-1], samples[nearestRank(0.99, n)-1]
}

// nearestRank returns the 1-based rank of the p-quantile (0<p<=1) over n
// sorted samples.
func nearestRank(p float64, n int) int {
	rank := int(math.Ceil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return rank
}

// isProtocolServerError separates provider/server failures from application
// errors. In particular, contract execution reverts commonly use -32000 and
// remain healthy responses rather than being treated as node failures.
func isProtocolServerError(err error) bool {
	var rpcErr rpc.Error
	if !errors.As(err, &rpcErr) {
		return false
	}
	switch rpcErr.ErrorCode() {
	case -32603, -32002, -32003:
		return true
	case -32000:
		message := strings.ToLower(rpcErr.Error())
		return !strings.Contains(message, "execution reverted") &&
			!strings.Contains(message, "revert")
	default:
		return false
	}
}

package pool

import (
	"context"
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

// Scheduling defaults deliberately start with headroom. Successful traffic
// lowers the pace slowly; explicit throttling or transport failure raises it
// quickly. These are implementation defaults, not capacity guarantees.
const (
	initialRequestPace = 20 * time.Millisecond
	minRequestPace     = 2 * time.Millisecond
	maxRequestPace     = 2 * time.Second
	requestPaceStep    = 4
	maxSourceInFlight  = 2
)

// monitor holds lightweight health and latency statistics for one Source.
// All methods are safe for concurrent use.
type monitor struct {
	mu sync.Mutex

	successes uint64
	failures  uint64
	// consecutiveFailures resets on success.
	consecutiveFailures uint64
	// rateLimited counts explicit provider throttling (HTTP 429, known
	// JSON-RPC throttle codes, or an equivalent rate-limit message): the
	// provider is alive but throttling us, which differs from being unreachable.
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

	// Scheduling state is kept with health observations so business calls,
	// references, confirmations and probes consume one source budget.
	schedulePace      time.Duration
	scheduleNext      time.Time
	scheduleInFlight  int
	scheduleSuccesses uint64
	scheduleWake      chan struct{}
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
	m.adaptSuccessLocked()
}

func (m *monitor) observeFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failures++
	m.consecutiveFailures++
	if isRateLimitError(err) {
		m.rateLimited++
	}
	if isProtocolServerError(err) {
		m.protocolServerErrors++
	}
	m.lastFailure = time.Now()
	m.lastError = err
	m.adaptFailureLocked(err)
}

// acquireRequest reserves one source-level scheduling slot. A caller that
// cannot obtain a slot before ctx expires has not made an upstream request.
// The release function must be called after the exchange settles.
func (m *monitor) acquireRequest(ctx context.Context) (func(), error) {
	for {
		m.mu.Lock()
		if m.schedulePace <= 0 {
			m.schedulePace = initialRequestPace
		}
		if m.scheduleWake == nil {
			m.scheduleWake = make(chan struct{})
		}
		now := time.Now()
		if m.scheduleInFlight < maxSourceInFlight &&
			(m.scheduleNext.IsZero() || !now.Before(m.scheduleNext)) {
			m.scheduleInFlight++
			m.scheduleNext = now.Add(m.schedulePace)
			m.mu.Unlock()
			return func() { m.releaseRequest() }, nil
		}

		wake := m.scheduleWake
		wait := 100 * time.Millisecond
		if m.scheduleInFlight < maxSourceInFlight && !m.scheduleNext.IsZero() {
			wait = time.Until(m.scheduleNext)
			if wait < 0 {
				wait = 0
			}
		}
		m.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (m *monitor) releaseRequest() {
	m.mu.Lock()
	if m.scheduleInFlight > 0 {
		m.scheduleInFlight--
	}
	m.signalScheduleLocked()
	m.mu.Unlock()
}

func (m *monitor) adaptSuccessLocked() {
	if m.schedulePace <= 0 {
		m.schedulePace = initialRequestPace
	}
	m.scheduleSuccesses++
	if m.scheduleSuccesses%requestPaceStep != 0 {
		return
	}
	// Add capacity slowly: reduce pace by at most five percent per step.
	step := m.schedulePace / 20
	if step < minRequestPace {
		step = minRequestPace
	}
	if m.schedulePace > minRequestPace+step {
		m.schedulePace -= step
	} else {
		m.schedulePace = minRequestPace
	}
	m.signalScheduleLocked()
}

func (m *monitor) adaptFailureLocked(err error) {
	if m.schedulePace <= 0 {
		m.schedulePace = initialRequestPace
	}
	if isRateLimitError(err) {
		m.schedulePace *= 2
	} else {
		m.schedulePace = m.schedulePace * 3 / 2
	}
	if m.schedulePace > maxRequestPace {
		m.schedulePace = maxRequestPace
	}
	// Push the next permit out as well, so a waiter cannot immediately issue
	// another request on a pre-throttle timestamp.
	next := time.Now().Add(m.schedulePace)
	if m.scheduleNext.Before(next) {
		m.scheduleNext = next
	}
	m.scheduleSuccesses = 0
	m.signalScheduleLocked()
}

func (m *monitor) signalScheduleLocked() {
	if m.scheduleWake == nil {
		m.scheduleWake = make(chan struct{})
		return
	}
	close(m.scheduleWake)
	m.scheduleWake = make(chan struct{})
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr rpc.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 429 {
		return true
	}
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) {
		switch rpcErr.ErrorCode() {
		case -32005, -32016, 429:
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "rate limit") ||
		strings.Contains(message, "too many requests") ||
		strings.Contains(message, "throttl")
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
		RequestPace:          m.schedulePace,
		RequestInFlight:      m.scheduleInFlight,
		RequestInFlightLimit: maxSourceInFlight,
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

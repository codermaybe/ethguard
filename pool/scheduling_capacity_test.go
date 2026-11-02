package pool

import (
	"errors"
	"testing"
	"time"
)

// quotaExceededProbe is an explicit provider throttle, not a capacity curve.
// The scheduler must treat it as a fast-backoff signal.
type quotaExceededProbe struct{}

func (quotaExceededProbe) Error() string     { return "provider quota exceeded: rate limit" }
func (quotaExceededProbe) IsRateLimit() bool { return true }

// serverFailureProbe is an availability/server failure with no quota signal.
// It backs off less aggressively than an explicit throttle.
type serverFailureProbe struct{}

func (serverFailureProbe) Error() string  { return "upstream internal error" }
func (serverFailureProbe) ErrorCode() int { return -32603 }

// TestSchedulePaceBacksOffFastAndRecoversSlowly pins the current controller's
// qualitative contract with deterministic state transitions: quota rejection
// doubles the pace, other failures grow it by half, recovery is a bounded
// slow-increase from the reduced pace, and idle traffic never inflates
// capacity.
func TestSchedulePaceBacksOffFastAndRecoversSlowly(t *testing.T) {
	var m monitor
	m.observeSuccess(time.Millisecond)
	if got := m.snapshot().RequestPace; got != initialRequestPace {
		t.Fatalf("initial pace = %s, want %s", got, initialRequestPace)
	}

	// Idle/low load: no observation means no capacity change.
	idle := m.snapshot().RequestPace
	if got := m.snapshot().RequestPace; got != idle {
		t.Fatalf("idle pace changed: %s -> %s", idle, got)
	}

	// Explicit quota rejection is the strongest signal.
	m.observeFailure(quotaExceededProbe{})
	afterQuota := m.snapshot().RequestPace
	if afterQuota != 2*initialRequestPace {
		t.Fatalf("quota backoff = %s, want %s", afterQuota, 2*initialRequestPace)
	}
	m.mu.Lock()
	nextPermit := m.scheduleNext
	m.mu.Unlock()
	if !nextPermit.After(time.Now()) {
		t.Fatal("quota backoff did not push the next permit out")
	}

	// A non-quota server failure backs off by half, not by a full doubling.
	m.observeFailure(serverFailureProbe{})
	afterServer := m.snapshot().RequestPace
	if afterServer != afterQuota*3/2 {
		t.Fatalf("server backoff = %s, want %s", afterServer, afterQuota*3/2)
	}
	if afterServer <= afterQuota {
		t.Fatalf("server failure did not reduce capacity: %s -> %s", afterQuota, afterServer)
	}

	// Sustained throttling is capped: repeated failures cannot grow the pace
	// without bound.
	for range 32 {
		m.observeFailure(quotaExceededProbe{})
	}
	if got := m.snapshot().RequestPace; got != maxRequestPace {
		t.Fatalf("sustained throttle pace = %s, want cap %s", got, maxRequestPace)
	}

	// Recovery: successes reduce the pace in small steps, never increase it,
	// and never leave the configured bounds. The first three successes after
	// a failure do not move the pace; the fourth starts the slow recovery.
	before := m.snapshot().RequestPace
	for i := 1; i <= 3; i++ {
		m.observeSuccess(time.Millisecond)
		if got := m.snapshot().RequestPace; got != before {
			t.Fatalf("recovery moved on success %d: %s -> %s", i, before, got)
		}
	}
	m.observeSuccess(time.Millisecond)
	afterStep := m.snapshot().RequestPace
	if afterStep >= before || afterStep < minRequestPace {
		t.Fatalf("recovery step out of bounds: %s -> %s", before, afterStep)
	}

	// Continued success recovers to, but not below, the minimum pace.
	for range 512 {
		m.observeSuccess(time.Millisecond)
		if got := m.snapshot().RequestPace; got < minRequestPace || got > maxRequestPace {
			t.Fatalf("recovery left bounds: %s", got)
		}
	}
	if got := m.snapshot().RequestPace; got != minRequestPace {
		t.Fatalf("recovery floor = %s, want %s", got, minRequestPace)
	}
}

// TestScheduleConcurrencyLimitIsFixedAndObservable pins the concurrency input
// the controller shares with callers: no success stream may raise the local
// in-flight limit, and the snapshot always reports the same bound.
func TestScheduleConcurrencyLimitIsFixedAndObservable(t *testing.T) {
	var m monitor
	for range 128 {
		m.observeSuccess(time.Millisecond)
	}
	if got := m.snapshot().RequestInFlightLimit; got != maxSourceInFlight {
		t.Fatalf("in-flight limit = %d, want %d", got, maxSourceInFlight)
	}
	if got := m.snapshot().RequestInFlight; got != 0 {
		t.Fatalf("idle in-flight = %d, want 0", got)
	}
	if maxSourceInFlight != 2 {
		t.Fatalf("scenario assumes the current two-slot budget, got %d", maxSourceInFlight)
	}
}

// TestScheduleAvailabilityFailuresOpenAndResetCircuit pins the availability
// signal path separately from capacity: repeated transport failures pause the
// source, and a later success resets the consecutive-failure count so a single
// probe can recover it without changing membership.
func TestScheduleAvailabilityFailuresOpenAndResetCircuit(t *testing.T) {
	var src Source
	for range eligibleFailureThreshold {
		src.stats.observeFailure(errors.New("transport reset"))
	}
	if src.requestable(time.Now()) {
		t.Fatal("repeated transport failures did not open the circuit")
	}

	src.stats.observeSuccess(time.Millisecond)
	if !src.requestable(time.Now()) {
		t.Fatal("a successful probe did not reset the circuit")
	}
	if got := src.stats.snapshot().ConsecutiveFailures; got != 0 {
		t.Fatalf("consecutive failures after success = %d, want 0", got)
	}
}

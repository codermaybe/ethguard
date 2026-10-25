package pool

import (
	"errors"
	"testing"
	"time"
)

type monitorHTTPStatusError struct{ status int }

func (e monitorHTTPStatusError) Error() string     { return "HTTP status" }
func (e monitorHTTPStatusError) IsRateLimit() bool { return e.status == 429 }

func TestMonitorPercentilesAreNearestRank(t *testing.T) {
	m := &monitor{}
	for i := 1; i <= 100; i++ {
		m.observeSuccess(time.Duration(i) * time.Millisecond)
	}
	snap := m.snapshot()
	if snap.LatencyP95 != 95*time.Millisecond {
		t.Fatalf("P95 = %v, want 95ms", snap.LatencyP95)
	}
	if snap.LatencyP99 != 99*time.Millisecond {
		t.Fatalf("P99 = %v, want 99ms", snap.LatencyP99)
	}
	if snap.LatencyEWMA == 0 || snap.LatencyEWMA >= snap.LatencyP99 {
		t.Fatalf("EWMA should track the bulk below the tail: %v", snap.LatencyEWMA)
	}
}

func TestMonitorWindowEvictsOldestSamples(t *testing.T) {
	m := &monitor{}
	for i := 1; i <= latencyWindow+10; i++ {
		m.observeSuccess(time.Duration(i) * time.Millisecond)
	}
	snap := m.snapshot()
	// The window holds samples 11..138; nearest-rank P99 over 128 samples is
	// rank 127, i.e. the second-newest value.
	if snap.LatencyP99 != 137*time.Millisecond {
		t.Fatalf("P99 after eviction = %v, want 137ms", snap.LatencyP99)
	}
}

func TestMonitorEmptyWindowReportsZeroPercentiles(t *testing.T) {
	m := &monitor{}
	snap := m.snapshot()
	if snap.LatencyP95 != 0 || snap.LatencyP99 != 0 {
		t.Fatalf("expected zero percentiles without samples, got %v/%v",
			snap.LatencyP95, snap.LatencyP99)
	}
}

func TestMonitorCountsRateLimitsDistinctly(t *testing.T) {
	m := &monitor{}
	m.observeFailure(monitorHTTPStatusError{status: 429})
	m.observeFailure(errors.New("connection reset"))
	m.observeSuccess(time.Millisecond)

	snap := m.snapshot()
	if snap.RateLimited != 1 {
		t.Fatalf("RateLimited = %d, want 1", snap.RateLimited)
	}
	if snap.Failures != 2 {
		t.Fatalf("429 must still count as a failure, Failures = %d", snap.Failures)
	}
	if snap.ConsecutiveFailures != 0 {
		t.Fatalf("success must reset the circuit regardless of throttle history")
	}
}

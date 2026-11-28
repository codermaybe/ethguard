package pool

import (
	"errors"
	"math"
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

func TestMonitorSingleSampleReportsItsLatency(t *testing.T) {
	m := &monitor{}
	m.observeSuccess(7 * time.Millisecond)

	snap := m.snapshot()
	if snap.LatencyP95 != 7*time.Millisecond || snap.LatencyP99 != 7*time.Millisecond {
		t.Fatalf("single-sample percentiles = %v/%v, want 7ms/7ms", snap.LatencyP95, snap.LatencyP99)
	}
}

func TestMonitorFullWindowSurvivesLargeSampleCounts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count uint64
	}{
		{name: "full_window", count: latencyWindow},
		{name: "below_max_int", count: math.MaxInt - 1},
		{name: "max_int", count: math.MaxInt},
		{name: "above_max_int", count: uint64(math.MaxInt) + 1},
		{name: "max_uint64", count: math.MaxUint64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("sample count %d caused panic: %v", tc.count, recovered)
				}
			}()
			m := &monitor{}
			for i := 1; i <= latencyWindow; i++ {
				m.observeSuccess(time.Duration(i) * time.Millisecond)
			}
			// Preserve the actual full window while injecting a lifetime count
			// that the former unbounded counter could eventually reach.
			m.rttN = tc.count
			snap := m.snapshot()
			if snap.LatencyP95 != 122*time.Millisecond || snap.LatencyP99 != 127*time.Millisecond {
				t.Fatalf("full-window percentiles = %v/%v, want 122ms/127ms", snap.LatencyP95, snap.LatencyP99)
			}
		})
	}
}

func TestMonitorFullWindowSurvivesSampleCountRollover(t *testing.T) {
	m := &monitor{}
	for i := 1; i <= latencyWindow; i++ {
		m.observeSuccess(time.Duration(i) * time.Millisecond)
	}
	m.rttN = math.MaxUint64
	m.observeSuccess(129 * time.Millisecond)

	snap := m.snapshot()
	if snap.LatencyP95 != 123*time.Millisecond || snap.LatencyP99 != 128*time.Millisecond {
		t.Fatalf("percentiles after replacing oldest sample = %v/%v, want 123ms/128ms", snap.LatencyP95, snap.LatencyP99)
	}
}

func TestMonitorSampleCountStopsAtWindowCapacity(t *testing.T) {
	m := &monitor{}
	for i := 1; i <= 2*latencyWindow; i++ {
		m.observeSuccess(time.Duration(i) * time.Millisecond)
	}
	if m.rttN != latencyWindow {
		t.Fatalf("populated sample count = %d, want %d", m.rttN, latencyWindow)
	}
	snap := m.snapshot()
	if snap.LatencyP95 != 250*time.Millisecond || snap.LatencyP99 != 255*time.Millisecond {
		t.Fatalf("percentiles after replacing full window = %v/%v, want 250ms/255ms", snap.LatencyP95, snap.LatencyP99)
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

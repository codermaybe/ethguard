package prometheus

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"ethguard/observe"
)

func TestRecordDecisionPopulatesFamilies(t *testing.T) {
	m := New()
	m.RecordDecision(observe.DecisionEvent{
		Method:   "eth_chainId",
		Status:   "corroborated",
		Duration: 12 * time.Millisecond,
		Attempts: []observe.SourceEvent{
			{Source: "alpha", RTT: 5 * time.Millisecond},
			{Source: "beta", RTT: 7 * time.Millisecond, Failed: true},
		},
	})

	if got := testutil.ToFloat64(m.decisions.WithLabelValues("eth_chainId", "corroborated")); got != 1 {
		t.Fatalf("decisions_total = %v, want 1", got)
	}
	if count := testutil.CollectAndCount(m, "ethguard_decision_duration_seconds"); count != 1 {
		t.Fatalf("expected one duration series, got %d", count)
	}
	if got := testutil.ToFloat64(m.failures.WithLabelValues("eth_chainId", "beta")); got != 1 {
		t.Fatalf("source_failures_total = %v, want 1", got)
	}
	if count := testutil.CollectAndCount(m, "ethguard_source_rtt_seconds"); count != 2 {
		t.Fatalf("expected one rtt series per source, got %d", count)
	}

	// Unset families must stay absent rather than emitting zero series.
	if count := testutil.CollectAndCount(m, "ethguard_conflicts_total"); count != 0 {
		t.Fatalf("expected no conflict series before any conflict, got %d", count)
	}
}

func TestRecordConflictAndFaults(t *testing.T) {
	m := New()
	m.RecordDecision(observe.DecisionEvent{
		Method:   "eth_chainId",
		Status:   "failed",
		Conflict: true,
		Faults:   2,
	})
	if got := testutil.ToFloat64(m.conflicts.WithLabelValues("eth_chainId")); got != 1 {
		t.Fatalf("conflicts_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.faults.WithLabelValues("eth_chainId")); got != 2 {
		t.Fatalf("faults_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.decisions.WithLabelValues("eth_chainId", "failed")); got != 1 {
		t.Fatalf("decisions_total{failed} = %v, want 1", got)
	}
}

func TestMetricsWorkInForeignRegistry(t *testing.T) {
	m := New()
	reg := prometheus.NewRegistry()
	if err := reg.Register(m); err != nil {
		t.Fatal(err)
	}
	m.RecordDecision(observe.DecisionEvent{Method: "anchor", Status: "selected"})
	if count := testutil.CollectAndCount(reg, "ethguard_decisions_total"); count != 1 {
		t.Fatalf("expected one decision series via foreign registry, got %d", count)
	}
}

func TestNilMetricsDropsEvent(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil Metrics panicked: %v", r)
		}
	}()
	var m *Metrics
	m.RecordDecision(observe.DecisionEvent{Method: "m", Status: "corroborated"})
}

func TestHandlerServesExposition(t *testing.T) {
	m := New()
	m.RecordDecision(observe.DecisionEvent{
		Method:   "eth_chainId",
		Status:   "corroborated",
		Duration: time.Millisecond,
		Attempts: []observe.SourceEvent{{Source: "alpha", RTT: time.Millisecond}},
	})

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("handler status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"ethguard_decisions_total", "ethguard_source_rtt_seconds"} {
		if !strings.Contains(body, want) {
			t.Fatalf("exposition missing %q:\n%s", want, body)
		}
	}
}

// Package prometheus turns EthGuard's observability events into Prometheus
// metrics. It is deliberately a separate package: applications that never
// import it do not link the Prometheus client.
//
// Metrics acts as both an observe.Recorder and a prometheus.Collector.
// Register it into any registry, or serve the built-in handler directly:
//
//	pm := prometheus.New()
//	client, err := ethguard.New(cfg, ethguard.WithRecorder(pm))
//	http.Handle("/metrics", pm.Handler())
//
// # Dashboard and alert migration
//
// Local dashboards and alerts are outside this repository. This is the
// label contract they should move to. Metric names, label names, and Help
// text are unchanged.
//
// ethguard_decisions_total status values that are actually emitted:
//
//	corroborated  event, metric, and rpc.Status.String(); successful cross-source evidence
//	selected      event, metric, and rpc.Status.String(); successful weaker class
//	forwarded     event, metric, and rpc.Status.String(); successful single-source class
//	failed        event and metric only
//
// "failed" is not an rpc.Status value and is never Decision.Status. A failed
// call is Decision.Status Unknown plus a non-nil error. rpc.Status.String
// also returns "unknown", but a failed decision is recorded as "failed",
// not as "unknown". Do not look for a Failed confidence class on the API.
//
// Alerts should watch the rate of ethguard_conflicts_total and the rate of
// ethguard_decisions_total{status="failed"}. Do not alert on the absence of
// status="corroborated": selected and forwarded are successful classes, and
// a quiet method is not a failure. Disagreement is conflicts_total (and the
// event Conflict flag), not a status label. A conflict also increments
// status="failed"; the disagreement signal itself stays on conflicts_total.
//
// Cardinality stays bounded. method is the caller-supplied JSON-RPC name,
// or the literal "anchor", and is limited by configuration
// (observe.DecisionEvent.Method). source is the configured endpoint name,
// never a value taken from a response (observe.SourceEvent.Source).
//
// Documented emitted status labels: corroborated, selected, forwarded, failed.
package prometheus

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"ethguard/observe"
)

// Metrics aggregates decision and per-source events into six metric
// families. It must be created with New; the zero value is unusable.
type Metrics struct {
	decisions *prometheus.CounterVec   // method, status
	conflicts *prometheus.CounterVec   // method
	faults    *prometheus.CounterVec   // method
	duration  *prometheus.HistogramVec // method
	rtt       *prometheus.HistogramVec // method, source
	failures  *prometheus.CounterVec   // method, source

	registry *prometheus.Registry
}

// New builds the metric set. The returned value can be registered into any
// number of registries; each call also owns a private registry backing
// Handler.
func New() *Metrics {
	m := &Metrics{
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethguard",
			Name:      "decisions_total",
			Help:      "Aggregate decisions by method and confidence class (corroborated, selected, forwarded, failed).",
		}, []string{"method", "status"}),
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethguard",
			Name:      "conflicts_total",
			Help:      "Decisions rejected because sources disagreed under corroboration.",
		}, []string{"method"}),
		faults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethguard",
			Name:      "faults_total",
			Help:      "Collection-phase faults (source failures, normalize errors) attached to decisions.",
		}, []string{"method"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "ethguard",
			Name:      "decision_duration_seconds",
			Help:      "Wall time from prepare through resolve per aggregate decision.",
		}, []string{"method"}),
		rtt: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "ethguard",
			Name:      "source_rtt_seconds",
			Help:      "Per-source round-trip time inside a fan-out.",
		}, []string{"method", "source"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "ethguard",
			Name:      "source_failures_total",
			Help:      "Per-source attempts that contributed no value to their decision.",
		}, []string{"method", "source"}),
	}
	m.registry = prometheus.NewRegistry()
	m.registry.MustRegister(m)
	return m
}

// RecordDecision implements observe.Recorder. Per-source attempts are
// accounted here as well; there is no separate per-attempt hook. A nil
// receiver drops the event so a miswired recorder degrades silently instead
// of panicking the call path.
func (m *Metrics) RecordDecision(e observe.DecisionEvent) {
	if m == nil {
		return
	}
	m.decisions.WithLabelValues(e.Method, e.Status).Inc()
	if e.Conflict {
		m.conflicts.WithLabelValues(e.Method).Inc()
	}
	if e.Faults > 0 {
		m.faults.WithLabelValues(e.Method).Add(float64(e.Faults))
	}
	m.duration.WithLabelValues(e.Method).Observe(e.Duration.Seconds())
	for _, attempt := range e.Attempts {
		m.rtt.WithLabelValues(e.Method, attempt.Source).Observe(attempt.RTT.Seconds())
		if attempt.Failed {
			m.failures.WithLabelValues(e.Method, attempt.Source).Inc()
		}
	}
}

// Describe implements prometheus.Collector.
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	m.decisions.Describe(ch)
	m.conflicts.Describe(ch)
	m.faults.Describe(ch)
	m.duration.Describe(ch)
	m.rtt.Describe(ch)
	m.failures.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	m.decisions.Collect(ch)
	m.conflicts.Collect(ch)
	m.faults.Collect(ch)
	m.duration.Collect(ch)
	m.rtt.Collect(ch)
	m.failures.Collect(ch)
}

// Handler serves Prometheus text exposition over this metric set alone,
// suitable for http.Handle("/metrics", ...).
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

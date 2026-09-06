// Package observe is the observability seam between EthGuard and its host
// application. The core stays dependency-free by speaking in plain events:
// hosts inject a structured logger (NewLog) or their own Recorder; the
// optional ethguard/observe/prometheus adapter turns the same events into
// Prometheus metrics without coupling the rest of the module to that client.
// Event assembly lives with the caller that owns the decisions (the
// aggregator), which keeps this package free of pool and policy types.
package observe

import "time"

// Recorder receives exactly one event per aggregate decision, with every
// per-source attempt folded into it. Implementations must be safe for
// concurrent use and must not block or panic the call path.
type Recorder interface {
	RecordDecision(DecisionEvent)
}

// DecisionEvent describes one completed aggregation: what was asked, what
// confidence class came back, and how much disagreement or breakage occurred
// along the way.
type DecisionEvent struct {
	// Method is the JSON-RPC method name as the caller addressed it, or the
	// literal "anchor" when the event reports an anchored read establishing
	// its block reference. Label cardinality is bounded by configuration.
	Method string
	// Status is one of "verified", "selected", "forwarded" (matching
	// rpc.Status.String()) or "failed" when no classified decision was
	// reached. A failed decision may still carry evidence and faults.
	Status string
	// Conflict marks a resolution rejected because sources disagreed rather
	// than because they were unreachable.
	Conflict bool
	// Faults counts collection-phase problems that did not necessarily
	// prevent the decision: hung sources, normalize failures, pool-level
	// collection errors.
	Faults int
	// Attempts holds one entry per source the request fanned out to,
	// including failed calls but excluding calls that never got to start.
	Attempts []SourceEvent
	// Duration covers prepare, fan-out, and resolve for this decision.
	Duration time.Duration
}

// SourceEvent describes one upstream attempt inside a fan-out.
type SourceEvent struct {
	// Source is the display name of the endpoint. Names come from caller
	// configuration, never from responses, keeping label cardinality bounded.
	Source string
	// RTT is the observed round-trip time; zero means the call never started.
	RTT time.Duration
	// Failed means the attempt supplied no answer usable by the method policy.
	// Valid dissent and accepted execution reverts are not source failures.
	Failed bool
}

// Nop discards every event. It is the implicit recorder when none is wired.
var Nop Recorder = nop{}

type nop struct{}

func (nop) RecordDecision(DecisionEvent) {}

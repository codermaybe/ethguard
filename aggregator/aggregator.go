// Package aggregator resolves raw multi-source observations using method
// policies. It does not own connections or source health.
package aggregator

import (
	"context"
	"fmt"
	"time"

	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
)

// Aggregator is the small orchestration seam between pool and method policy.
type Aggregator struct {
	pool     *pool.Pool
	policies rpc.Registry
	rec      observe.Recorder
}

// Option tunes an Aggregator at construction time.
type Option func(*Aggregator)

// WithRecorder routes one observability event per aggregate decision into r.
// A nil recorder keeps the default no-op sink.
func WithRecorder(r observe.Recorder) Option {
	return func(a *Aggregator) {
		if r != nil {
			a.rec = r
		}
	}
}

func New(p *pool.Pool, policies rpc.Registry, opts ...Option) *Aggregator {
	a := &Aggregator{pool: p, policies: policies, rec: observe.Nop}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Aggregate collects one request and resolves it according to its policy.
//
// The returned error reports why no classified decision was reached.
// Failures that did not prevent a decision — a pool-level collection error
// or individual sources failing or timing out — are attached to
// Decision.Faults instead, keeping partial fan-outs diagnosable.
func (a *Aggregator) Aggregate(ctx context.Context, req rpc.Request) (rpc.Decision, error) {
	started := time.Now()
	policy := a.policies.PolicyFor(req.Method)
	prepared, err := policy.Prepare(ctx, req)
	if err != nil {
		a.rec.RecordDecision(observe.DecisionEvent{
			Method:   req.Method,
			Status:   "failed",
			Duration: time.Since(started),
		})
		return rpc.Decision{}, err
	}
	emissions, collectErr := a.pool.Collect(ctx, prepared.Method, prepared.Params, pool.CollectOptions{})
	observations := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{Source: emission.Src, Value: emission.Val, Err: emission.Err}
	}
	decision, resolveErr := policy.Resolve(observations)
	if collectErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("collect: %w", collectErr))
	}
	for _, obs := range observations {
		if obs.Err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("source %q: %w", obs.Source.Name(), obs.Err))
		}
	}
	a.rec.RecordDecision(observe.BuildDecision(started, req.Method, decision, resolveErr, emissions))
	return decision, resolveErr
}

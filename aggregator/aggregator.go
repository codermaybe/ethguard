// Package aggregator resolves raw multi-source observations using method
// policies. It does not own connections or source health.
package aggregator

import (
	"context"
	"fmt"

	"ethguard/pool"
	"ethguard/rpc"
)

// Aggregator is the small orchestration seam between pool and method policy.
type Aggregator struct {
	pool     *pool.Pool
	policies rpc.Registry
}

func New(p *pool.Pool, policies rpc.Registry) *Aggregator {
	return &Aggregator{pool: p, policies: policies}
}

// Aggregate collects one request and resolves it according to its policy.
//
// The returned error reports why no classified decision was reached.
// Failures that did not prevent a decision — a pool-level collection error
// or individual sources failing or timing out — are attached to
// Decision.Faults instead, keeping partial fan-outs diagnosable.
func (a *Aggregator) Aggregate(ctx context.Context, req rpc.Request) (rpc.Decision, error) {
	policy := a.policies.PolicyFor(req.Method)
	prepared, err := policy.Prepare(ctx, req)
	if err != nil {
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
	return decision, resolveErr
}

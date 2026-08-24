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
	if resolveErr != nil {
		if collectErr != nil {
			return decision, fmt.Errorf("aggregate: collect: %w", collectErr)
		}
		return decision, resolveErr
	}
	return decision, nil
}

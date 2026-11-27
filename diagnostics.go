package ethguard

import (
	"context"

	"github.com/codermaybe/ethguard/internal/aggregator"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

// SourceSnapshot is a value copy of one configured member's identity and health.
// Error causes in Health are immutable shared values.
type SourceSnapshot struct {
	Source rpc.SourceRef
	Health observe.HealthSnapshot
}

// Sources returns configured members in construction order, including members
// currently paused by scheduling gates. Each call owns its returned slice.
func (c *Client) Sources() []SourceSnapshot {
	sources := c.pool.Sources()
	snapshots := make([]SourceSnapshot, len(sources))
	for i, source := range sources {
		snapshots[i] = SourceSnapshot{Source: aggregator.SourceRefOf(source), Health: source.Health()}
	}
	return snapshots
}

// Sample sends one raw diagnostic request to each currently eligible source.
// It uses the collection timeout and the default cooldown and latest-tip gates,
// accounts transport health, and returns source order without method admission,
// reference establishment, or adjudication. Evidence is empty. Observations are
// retained when collection also returns an error.
//
// Parameters are frozen before dispatch. Returned result and protocol-error
// bytes are complete and caller-owned; error causes are immutable shared values.
func (c *Client) Sample(ctx context.Context, method string, params ...any) ([]rpc.Observation, error) {
	ctx, done := c.operationContext(ctx)
	defer done()
	if c.isClosed() {
		return nil, ErrPoolClosed
	}
	frozen, _, err := rpcengine.FreezeParams(params)
	if err != nil {
		return nil, err
	}
	emissions, err := c.pool.Collect(ctx, method, frozen, pool.CollectOptions{Timeout: c.collectTimeout})
	observations := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{Attempted: emission.Attempted, Source: aggregator.SourceRefOf(emission.Src), Response: emission.Response.Clone()}
	}
	return observations, mapLifecycleError(ctx, err)
}

// Package aggregator resolves raw multi-source observations using method
// policies. It does not own connections or source health.
package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
)

// Aggregator is the small orchestration seam between pool and method policy.
type Aggregator struct {
	pool           *pool.Pool
	policies       rpc.Registry
	rec            observe.Recorder
	collectTimeout time.Duration
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

// WithCollectTimeout bounds the pool operation used by each aggregate call.
// Zero keeps pool's default timeout.
func WithCollectTimeout(timeout time.Duration) Option {
	return func(a *Aggregator) { a.collectTimeout = timeout }
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
// Failures that did not prevent a decision — a partial pool-level collection
// error or individual attempted sources failing or timing out — are attached
// to Decision.Faults instead, keeping partial fan-outs diagnosable. A terminal
// collection error with no usable emission is returned directly so the pool's
// public sentinel is not masked by a policy's empty-input error.
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
	var emissions []pool.Emission
	var collectErr error
	if single, ok := policy.(interface{ SingleSource() bool }); ok && single.SingleSource() {
		emissions, collectErr = a.pool.Forward(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout})
	} else {
		emissions, collectErr = a.pool.Collect(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout})
	}
	observations := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{
			Source:   rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()},
			Value:    emission.Val,
			Err:      emission.Err,
			RPCError: observationRPCError(emission.Err),
		}
	}
	if collectErr != nil && !hasUsableEmission(emissions) {
		decision := rpc.Decision{Faults: collectionFaults(collectErr, emissions)}
		a.rec.RecordDecision(observe.BuildDecision(started, req.Method, decision, collectErr, emissions))
		return decision, collectErr
	}
	decision, resolveErr := policy.Resolve(observations)
	if provider, ok := policy.(rpc.BlockRefProvider); ok {
		decision.BlockRef = provider.BlockRef(req, prepared)
	}
	if collectErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("collect: %w", collectErr))
	}
	for _, emission := range emissions {
		if emission.Attempted && emission.Err != nil && !isRPCResponseError(emission.Err) {
			decision.Faults = append(decision.Faults, fmt.Errorf("source %q: %w", emission.Src.Name(), emission.Err))
		}
	}
	a.rec.RecordDecision(observe.BuildDecision(started, req.Method, decision, resolveErr, emissions))
	return decision, resolveErr
}

func hasUsableEmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if emission.Val != nil {
			return true
		}
		var rpcErr gethrpc.Error
		if errors.As(emission.Err, &rpcErr) {
			return true
		}
	}
	return false
}

func collectionFaults(collectErr error, emissions []pool.Emission) []error {
	faults := []error{fmt.Errorf("collect: %w", collectErr)}
	for _, emission := range emissions {
		if emission.Attempted && emission.Err != nil && !isRPCResponseError(emission.Err) {
			faults = append(faults, fmt.Errorf("source %q: %w", emission.Src.Name(), emission.Err))
		}
	}
	return faults
}

func observationRPCError(err error) *rpc.RPCError {
	if err == nil {
		return nil
	}
	var protocolErr gethrpc.Error
	if !errors.As(err, &protocolErr) {
		return nil
	}
	structured := &rpc.RPCError{Code: protocolErr.ErrorCode(), Message: protocolErr.Error()}
	var dataErr gethrpc.DataError
	if errors.As(err, &dataErr) {
		if data := dataErr.ErrorData(); data != nil {
			if raw, marshalErr := json.Marshal(data); marshalErr == nil {
				structured.Data = raw
			}
		}
	}
	return structured
}

func isRPCResponseError(err error) bool {
	var rpcErr gethrpc.Error
	return errors.As(err, &rpcErr)
}

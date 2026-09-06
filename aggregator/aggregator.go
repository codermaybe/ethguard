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

	"ethguard/internal/refcontext"
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
	ctx, capture := refcontext.New(ctx)
	policy := a.policies.PolicyFor(req.Method)
	prepared, err := policy.Prepare(ctx, req)
	if err != nil {
		decision := rpc.Decision{Reference: capture.Reference()}
		a.recordDecision(started, req.Method, policy, decision, err, nil)
		return decision, err
	}
	var emissions []pool.Emission
	var collectErr error
	if single, ok := policy.(rpc.SingleSourcePolicy); ok && single.SingleSource() {
		emissions, collectErr = a.pool.Forward(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout})
	} else {
		emissions, collectErr = a.pool.Collect(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout})
	}
	observations := make([]rpc.Observation, len(emissions))
	participants := make([]rpc.SourceRef, 0, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{
			Source:   rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()},
			Value:    emission.Val,
			Err:      emission.Err,
			RPCError: observationRPCError(emission.Err),
		}
		participants = append(participants, observations[i].Source)
	}
	if collectErr != nil && !hasUsableEmission(emissions) {
		decision := rpc.Decision{Faults: collectionFaults(policy, collectErr, emissions), Reference: capture.Reference(), Participants: participants}
		a.recordDecision(started, req.Method, policy, decision, collectErr, emissions)
		return decision, collectErr
	}
	decision, resolveErr := policy.Resolve(observations)
	if agreement, ok := policy.(rpc.AgreementPolicy); ok && agreement.RequiresAgreement() && refcontext.VerificationRequired(ctx) && resolveErr == nil && decision.Status != rpc.Verified {
		resolveErr = fmt.Errorf("typed read requires corroboration: %w", rpc.ErrConflict)
	}
	decision.Reference = capture.Reference()
	decision.Participants = participants
	if provider, ok := policy.(rpc.BlockRefProvider); ok {
		decision.BlockRef = provider.BlockRef(req, prepared)
	}
	if collectErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("collect: %w", collectErr))
	}
	decision.Faults = append(decision.Faults, sourceFaults(policy, emissions)...)
	a.recordDecision(started, req.Method, policy, decision, resolveErr, emissions)
	return decision, resolveErr
}

// recordDecision folds one completed aggregation into exactly one
// observability event (decision 0008). It lives here — the call site that
// owns policies — so the per-attempt Failed flag reuses the policy's own
// validity contract instead of a second answer classifier living in the
// observability seam. The no-op sink skips assembly entirely, keeping the
// default call path free of event-building costs.
func (a *Aggregator) recordDecision(started time.Time, method string, policy rpc.Policy, decision rpc.Decision, resolveErr error, emissions []pool.Emission) {
	if a.rec == observe.Nop {
		return
	}
	event := observe.DecisionEvent{
		Method:   method,
		Duration: time.Since(started),
		Faults:   len(decision.Faults),
	}
	if resolveErr != nil {
		// resolveErr is authoritative: a decision carrying evidence but
		// rejected (for example by conflict detection) reports "failed"
		// with Conflict set, never a success class.
		event.Status = "failed"
		event.Conflict = errors.Is(resolveErr, rpc.ErrConflict)
	} else {
		event.Status = decision.Status.String()
	}
	for _, emission := range emissions {
		if !emission.Attempted {
			continue // never acquired a slot: no attempt, no observation
		}
		event.Attempts = append(event.Attempts, observe.SourceEvent{
			Source: emission.Src.Name(),
			RTT:    emission.RTT,
			Failed: !usableAnswer(policy, emission),
		})
	}
	a.rec.RecordDecision(event)
}

// usableAnswer reports whether an attempted call provided an answer the
// policy could use. A delivered value counts when the policy normalizes it
// — a valid answer that simply lost the comparison is dissent, not a
// failure. A structured protocol error counts only for policies that opted
// into error bodies (rpc.RPCErrorPolicy) and accept the body: a verified
// revert is an observation that contributed value. Everything else —
// transport faults, timeouts, malformed payloads, protocol errors the
// policy rejects — provided no usable answer.
func usableAnswer(policy rpc.Policy, emission pool.Emission) bool {
	if emission.Err == nil {
		_, err := policy.Normalize(emission.Val)
		return err == nil
	}
	structured := observationRPCError(emission.Err)
	if structured == nil {
		return false
	}
	if provider, ok := policy.(rpc.RPCErrorPolicy); ok && provider.IncludeRPCError() {
		_, err := policy.Normalize(structured.Raw())
		return err == nil
	}
	return false
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

func collectionFaults(policy rpc.Policy, collectErr error, emissions []pool.Emission) []error {
	return append([]error{fmt.Errorf("collect: %w", collectErr)}, sourceFaults(policy, emissions)...)
}

// sourceFaults preserves rejected protocol answers as diagnostics, without
// changing pool health or blaming calls that never started.
func sourceFaults(policy rpc.Policy, emissions []pool.Emission) []error {
	var faults []error
	for _, emission := range emissions {
		if emission.Attempted && emission.Err != nil && !usableAnswer(policy, emission) {
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

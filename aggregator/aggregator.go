// Package aggregator resolves raw multi-source observations using method
// policies. It does not own connections or source health.
package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"ethguard/internal/jsonsafe"
	"ethguard/internal/refcontext"
	"ethguard/internal/uncertain"
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
	// corrob is the runtime-mutable adjudication configuration shared by one
	// client's aggregators; nil leaves policies on construction-time
	// defaults (direct construction, legacy tests).
	corrob *rpc.CorroborationShared
}

// Option tunes an Aggregator at construction time.
type Option func(*Aggregator)

// WithRecorder routes one observability event per aggregate decision into r.
func WithRecorder(r observe.Recorder) Option {
	return func(a *Aggregator) {
		if r != nil {
			a.rec = r
		}
	}
}

// WithCollectTimeout bounds the pool operation used by each aggregate call.
func WithCollectTimeout(timeout time.Duration) Option {
	return func(a *Aggregator) { a.collectTimeout = timeout }
}

// WithCorroboration shares one runtime-mutable corroboration configuration
// across the client's aggregators. Each invocation snapshots the mode and
// support floor at its start (decision 0025); corroborated reads then collect
// the full voting basis and resolve under the snapshot instead of
// construction-time defaults.
func WithCorroboration(c *rpc.CorroborationShared) Option {
	return func(a *Aggregator) {
		if c != nil {
			a.corrob = c
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

// corroboration captures the shared configuration for one invocation. A nil
// shared configuration yields no snapshot, so the aggregator falls back to
// each policy's construction-time resolution.
func (a *Aggregator) corroboration() (rpc.Corroboration, bool) {
	if a.corrob == nil {
		return rpc.Corroboration{}, false
	}
	return a.corrob.Snapshot(), true
}

// resolve applies the invocation's frozen corroboration snapshot when the
// policy supports it; policies without the capability resolve with their
// construction-time configuration.
func resolve(policy rpc.Policy, snap rpc.Corroboration, snapOK bool, obs []rpc.Observation) (rpc.Decision, error) {
	if snapOK {
		if cp, ok := policy.(rpc.CorroboratingPolicy); ok {
			return cp.ResolveCorroborated(snap, obs)
		}
	}
	return policy.Resolve(obs)
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
	ctx, uncertainDone := uncertain.Begin(ctx)
	defer uncertainDone()
	ctx, capture := refcontext.New(ctx)
	// The corroboration mode and support floor are frozen here, before
	// preparation and any request: a concurrent Set only reaches
	// invocations that start later (decision 0025).
	snap, snapOK := a.corroboration()
	if value, ok := refcontext.Corroboration(ctx); ok {
		if inherited, valid := value.(rpc.Corroboration); valid {
			snap, snapOK = inherited, true
		}
	} else if snapOK {
		ctx = refcontext.WithCorroboration(ctx, snap)
	}
	var members []*pool.Source
	if value, ok := refcontext.Members(ctx); ok {
		members, _ = value.([]*pool.Source)
	}
	if members == nil {
		members = a.pool.VotingMembers()
		ctx = refcontext.WithMembers(ctx, members)
	}
	policy := a.policies.PolicyFor(req.Method)
	// A corroborated read collects over the fixed voting basis — every
	// member occupies the denominator even when a scheduling gate skipped
	// its request (decisions 0025 and 0027).
	_, corroborating := policy.(rpc.CorroboratingPolicy)
	corroborating = snapOK && corroborating
	prepared, err := policy.Prepare(ctx, req)
	if err != nil {
		if c := uncertain.FromContext(ctx); c != nil {
			c.Record("prepare", req.Method, rpc.SourceRef{}, nil, err.Error())
		}
		decision := rpc.Decision{Reference: capture.Reference()}
		a.recordDecision(started, req.Method, policy, decision, err, nil)
		return decision, err
	}
	var emissions []pool.Emission
	var collectErr error
	if snapOK && snap.Mode == rpc.CorroborationForwarded {
		emissions, collectErr = a.pool.Forward(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout, Sources: members})
	} else if single, ok := policy.(rpc.SingleSourcePolicy); ok && single.SingleSource() {
		emissions, collectErr = a.pool.Forward(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout, Sources: members})
	} else if corroborating {
		emissions, collectErr = a.pool.CollectVoting(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout, Sources: members})
	} else {
		emissions, collectErr = a.pool.Collect(ctx, prepared.Method, prepared.Params, pool.CollectOptions{Timeout: a.collectTimeout, Sources: members})
	}
	// Preserve every primary response before admission is allowed to clear a
	// malformed value. This is the raw per-source record used by Uncertain;
	// admission and resolver outcomes are recorded as separate stages below.
	if c := uncertain.FromContext(ctx); c != nil {
		for _, emission := range emissions {
			source := rpc.SourceRef{}
			if emission.Src != nil {
				source = rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()}
			}
			errText := ""
			if emission.Err != nil {
				errText = emission.Err.Error()
			}
			c.Record("response", prepared.Method, source, emission.Val, errText)
		}
		if collectErr != nil {
			c.Record("collect", prepared.Method, rpc.SourceRef{}, nil, collectErr.Error())
		}
	}
	// Reject structurally unsafe JSON before any method-specific admission or
	// normalization. The raw response has already been captured above so the
	// caller can still inspect the offending source through UncertainCapture.
	for i := range emissions {
		if emissions[i].Err != nil || emissions[i].Val == nil {
			continue
		}
		if err := jsonsafe.Validate(emissions[i].Val); err != nil {
			emissions[i].Err = fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
			emissions[i].Val = nil
		}
	}
	// Admission runs before normalization and clustering. A malformed body
	// cannot win a cluster, and dependent content is always fetched from the
	// source that supplied the primary response.
	if admission, ok := policy.(rpc.AdmissionPolicy); ok {
		var wg sync.WaitGroup
		for i := range emissions {
			if emissions[i].Err != nil || emissions[i].Val == nil {
				continue
			}
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				evidence, admissionErr := admission.Admit(ctx, prepared, emissions[i].Val, func(callCtx context.Context, method string, params []any) (json.RawMessage, error) {
					raw, callErr := a.pool.CallSource(callCtx, emissions[i].Src, method, params)
					if c := uncertain.FromContext(ctx); c != nil {
						source := rpc.SourceRef{Name: emissions[i].Src.Name(), URL: emissions[i].Src.URL(), ID: emissions[i].Src.ID()}
						errText := ""
						if callErr != nil {
							errText = callErr.Error()
						}
						c.Record("dependency", method, source, raw, errText)
					}
					return raw, callErr
				})
				if admissionErr != nil {
					if c := uncertain.FromContext(ctx); c != nil {
						source := rpc.SourceRef{Name: emissions[i].Src.Name(), URL: emissions[i].Src.URL(), ID: emissions[i].Src.ID()}
						c.Record("admission", prepared.Method, source, nil, admissionErr.Error())
					}
					emissions[i].Err = fmt.Errorf("admission: %w", admissionErr)
					emissions[i].Val = nil
					return
				}
				emissions[i].Evidence = evidence
			}()
		}
		wg.Wait()
	}
	observations := make([]rpc.Observation, len(emissions))
	participants := make([]rpc.SourceRef, 0, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{
			Source:   rpc.SourceRef{Name: emission.Src.Name(), URL: emission.Src.URL(), ID: emission.Src.ID()},
			Value:    emission.Val,
			Err:      emission.Err,
			RPCError: observationRPCError(emission.Err),
			Evidence: emission.Evidence,
		}
		participants = append(participants, observations[i].Source)
	}
	if collectErr != nil && !hasUsableEmission(emissions) {
		decision := rpc.Decision{Faults: collectionFaults(policy, collectErr, emissions), Reference: capture.Reference(), Participants: participants}
		a.recordDecision(started, req.Method, policy, decision, collectErr, emissions)
		return decision, collectErr
	}
	// Admission runs before clustering, so when every delivered response is
	// malformed there is no usable cluster from which Strict could preserve
	// the more specific malformed-response class. Keep that class visible to
	// typed callers instead of replacing it with ErrNoValidResponse.
	if collectErr == nil && !hasUsableEmission(emissions) && hasMalformedAdmission(emissions) {
		admissionErr := firstMalformedAdmission(emissions)
		decision := rpc.Decision{Faults: sourceFaults(policy, emissions), Reference: capture.Reference(), Participants: participants}
		err := fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, admissionErr)
		a.recordDecision(started, req.Method, policy, decision, err, emissions)
		return decision, err
	}
	decision, resolveErr := resolve(policy, snap, snapOK, observations)
	if resolveErr == nil && snapOK && snap.Mode == rpc.CorroborationForwarded {
		decision.Status = rpc.Forwarded
	}
	if agreement, ok := policy.(rpc.AgreementPolicy); ok && agreement.RequiresAgreement() && refcontext.VerificationRequired(ctx) && !(snapOK && snap.Mode == rpc.CorroborationForwarded) && resolveErr == nil && decision.Status != rpc.Verified {
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

func hasMalformedAdmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if errors.Is(emission.Err, rpc.ErrMalformedResponse) {
			return true
		}
	}
	return false
}

func firstMalformedAdmission(emissions []pool.Emission) error {
	for _, emission := range emissions {
		if errors.Is(emission.Err, rpc.ErrMalformedResponse) {
			return emission.Err
		}
	}
	return rpc.ErrMalformedResponse
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

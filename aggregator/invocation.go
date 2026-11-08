package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// The read pipeline is a sequence of named phases over one invocation:
//
//	Prepare → ReferenceStage → PrepareWithReference → Collect → Admit → Resolve → Project
//
// One logical call owns one Invocation (one ID, one frozen Basis) from start
// to finish. The reference rounds of a moving-tag read run inside the same
// invocation through the ReferenceStage — the same members, the same
// thresholds, the same error assembly as the business round, and no nested
// aggregate. "One Aggregate" is one coordinator, not one network round: the
// reference establishment may run several rounds before the business fan-out.

// invocationState is one read invocation's working state across the named
// phases. Fields fill in phase order; nothing writes backwards. A read
type invocationState struct {
	ctx        context.Context
	invocation rpc.Invocation
	// plan is the explicit execution plan of one read: the plan value owns
	// every routing decision (prepare, reference, pending, collection,
	// resolution, projection).
	plan *rpc.ReadPlan
	// planPin is the block reference the plan's rewrite introduced, recorded
	// at prepare time for the projection phase.
	planPin string
	snap    rpc.Corroboration
	snapOK  bool
	// corroborating selects the fixed-basis collection mode: the full voting
	// membership occupies the denominator even when a scheduling gate skipped
	// its request (decisions 0025 and 0062).
	corroborating bool
	members       []*pool.Source
	capture       *refcontext.Capture

	prepared rpc.Request
	// wireParams is the frozen parameter list the business fan-out sends:
	// the one controlled serialization from prepare, so the wire and the
	// recorded prepared-request fact stay the same bytes. preparedJSON is
	// the joined array snapshot carried on Decision.Prepared.
	wireParams   []any
	preparedJSON json.RawMessage
	emissions    []pool.Emission
	collectErr   error

	observations []rpc.Observation
	participants []rpc.SourceRef

	// evaluation/aggregateErr/aggregateFaults carry the field-aggregation
	// outcome; the error itself is kept so every return path can attach it to
	// Faults instead of dropping it silently.
	evaluation      *rpc.Evaluation
	aggregateErr    error
	aggregateFaults []error

	verdicts []rpc.SourceVerdict
}

// validityPolicy is the policy whose validity contract classifies answers
// and attempts: the plan's resolver value. Normalization and the
// RPC-error-body opt-in live on it.
func (state *invocationState) validityPolicy() rpc.Policy {
	return state.plan.Resolver
}

// admissionHook is the method-owned admission hook, or absent when the read
// declares none.
func (state *invocationState) admissionHook() (func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error), bool) {
	return plannedAdmissionHook(state.plan)
}

// requiresAgreement reports whether the read's resolution claims a
// corroboration-bound agreement: the resolver's own capability.
func (state *invocationState) requiresAgreement() bool {
	agreement, ok := state.validityPolicy().(rpc.AgreementPolicy)
	return ok && agreement.RequiresAgreement()
}

// pendingLocalRoute is the single pending-local routing check: a pending
// request on a pending-observation read resolves as a node-local
// observation, except in Forwarded mode, where the pending shape still
// forwards.
func (state *invocationState) pendingLocalRoute() bool {
	return planPendingLocalRoute(state.plan, state.prepared, state.snap, state.snapOK)
}

// resolve runs the plan's declared resolution semantics.
func (state *invocationState) resolve() invocationResolution {
	return resolvePlannedInvocation(state.plan, state.prepared, state.snap, state.snapOK, state.observations)
}

// earlyExit carries one degraded phase result: a classified decision plus the
// error that explains why no resolved value was reached.
type earlyExit struct {
	decision rpc.Decision
	err      error
}

// beginInvocation freezes the read invocation: one ID, the voting basis
// captured before any request, the corroboration snapshot, and the raw-data
// capture. The typed facade's corroboration requirement rides the invocation
// itself (AggregateStrict); nothing rides the context.
func beginInvocation(snap rpc.Corroboration, snapOK bool, members []*pool.Source, capture *uncertain.Capture, requireCorroboration bool) rpc.Invocation {
	refs := make([]rpc.SourceRef, 0, len(members))
	for _, member := range members {
		refs = append(refs, SourceRefOf(member))
	}
	opts := []rpc.InvocationOption{rpc.WithInvocationCapture(capture.InvocationCapture())}
	if requireCorroboration {
		opts = append(opts, rpc.WithRequiredCorroboration())
	}
	return rpc.NewInvocation(rpc.NewInvocationID(), rpc.FreezeBasis(refs, snap), opts...)
}

// preparedTipFreshness reports whether the prepared business request still
// depends on the sampled latest tip. A moving tag rewritten by Prepare has
// become an explicit pin, so the business round is exempt: an explicit
// height/hash is valid regardless of the source's current tip. A request that
// never carried a moving tag is exempt for the same reason. Reference
// establishment is scoped separately by the runtime, where the requested tag
// is still known.
func preparedTipFreshness(state *invocationState) pool.TipFreshnessScope {
	request, needed := state.plan.ReferenceRequirement(state.prepared)
	if !needed || request.Kind != rpc.ReferenceLatest {
		return pool.TipFreshnessExempt
	}
	return pool.TipFreshnessLatest
}

// collectPhase fans the prepared request out under the invocation's collection
// mode: forwarded, single-source, fixed-basis voting, or symmetric collection.
// A planned read has no single-source shape; its collection mode follows the
// declared resolver class.
func (a *Aggregator) collectPhase(state *invocationState) {
	opts := pool.CollectOptions{
		Timeout:      a.collectTimeout,
		Sources:      state.members,
		TipFreshness: preparedTipFreshness(state),
	}
	forward := func() {
		state.emissions, state.collectErr = a.pool.Forward(state.ctx, state.prepared.Method, state.wireParams, opts)
	}
	switch {
	case state.snapOK && state.snap.Mode == rpc.CorroborationForwarded:
		// Intentional single-source forwarding: the configured mode, not the
		// method's declaration, decides.
		forward()
	case singleSourcePolicy(state.plan.Resolver):
		// The explicit forward plan (or a single-source declaration).
		forward()
	case state.corroborating:
		// A corroborated read collects over the fixed voting basis — every
		// member occupies the denominator even when a scheduling gate
		// skipped its request (decisions 0025 and 0062).
		state.emissions, state.collectErr = a.pool.CollectVoting(state.ctx, state.prepared.Method, state.wireParams, opts)
	default:
		state.emissions, state.collectErr = a.pool.Collect(state.ctx, state.prepared.Method, state.wireParams, opts)
	}
}

func singleSourcePolicy(policy rpc.Policy) bool {
	single, ok := policy.(rpc.SingleSourcePolicy)
	return ok && single.SingleSource()
}

// admitPhase preserves the raw responses, rejects structurally unsafe JSON,
// and runs the method's admission hook with a same-source caller. Admission
// runs before normalization and clustering: a malformed body cannot win a
// cluster, and dependent content is always fetched from the source that
// supplied the primary response.
func (a *Aggregator) admitPhase(state *invocationState) {
	// Preserve every primary response before admission is allowed to clear a
	// malformed value. This is the raw per-source record used by Uncertain;
	// admission and resolver outcomes are recorded as separate stages below.
	if c := uncertain.CollectorFromContext(state.ctx); c != nil {
		for _, emission := range state.emissions {
			source := SourceRefOf(emission.Src)
			errText := ""
			if emission.Err != nil {
				errText = emission.Err.Error()
			}
			c.Record("response", state.prepared.Method, source, emission.Val, errText)
		}
		if state.collectErr != nil {
			c.Record("collect", state.prepared.Method, rpc.SourceRef{}, nil, state.collectErr.Error())
		}
	}
	for i := range state.emissions {
		if state.emissions[i].Err != nil || state.emissions[i].Val == nil {
			continue
		}
		if err := jsonsafe.Validate(state.emissions[i].Val); err != nil {
			state.emissions[i].Err = fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
			state.emissions[i].Val = nil
		}
	}
	admission, ok := state.admissionHook()
	if !ok {
		return
	}
	var wg sync.WaitGroup
	for i := range state.emissions {
		if state.emissions[i].Err != nil || state.emissions[i].Val == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			evidence, admissionErr := admission(state.ctx, state.prepared, state.emissions[i].Val, func(callCtx context.Context, method string, params []any) (json.RawMessage, error) {
				raw, callErr := a.pool.CallSource(callCtx, state.emissions[i].Src, method, params)
				if c := uncertain.CollectorFromContext(state.ctx); c != nil {
					source := SourceRefOf(state.emissions[i].Src)
					errText := ""
					if callErr != nil {
						errText = callErr.Error()
					}
					c.Record("dependency", method, source, raw, errText)
				}
				return raw, callErr
			})
			if admissionErr != nil {
				if c := uncertain.CollectorFromContext(state.ctx); c != nil {
					c.Record("admission", state.prepared.Method, SourceRefOf(state.emissions[i].Src), nil, admissionErr.Error())
				}
				state.emissions[i].Err = fmt.Errorf("admission: %w", admissionErr)
				state.emissions[i].Val = nil
				return
			}
			state.emissions[i].Evidence = evidence
		}()
	}
	wg.Wait()
}

// resolvePhase assembles observations, aggregates fields, and adjudicates.
// A non-nil early exit reports the two degraded exits (collection failure
// with nothing usable, and every-delivered-response-malformed); otherwise the
// state carries the resolution outcome.
func (a *Aggregator) resolvePhase(state *invocationState, started time.Time) *earlyExit {
	state.observations = make([]rpc.Observation, len(state.emissions))
	state.participants = make([]rpc.SourceRef, 0, len(state.emissions))
	for i, emission := range state.emissions {
		state.observations[i] = rpc.Observation{
			Source:   SourceRefOf(emission.Src),
			Value:    emission.Val,
			Err:      emission.Err,
			RPCError: ObservationRPCError(emission.Err),
			Evidence: emission.Evidence,
		}
		state.participants = append(state.participants, state.observations[i].Source)
	}
	// A field-aggregation failure only degrades this invocation to a decision
	// without diagnostics; the error itself is kept so every return path
	// below can attach it to Faults instead of dropping it silently.
	if state.snapOK && state.snap.Mode != rpc.CorroborationForwarded {
		common, observationErr := rpc.AggregateFieldsWithCorroboration(state.observations, state.invocation.Basis())
		if observationErr == nil {
			state.evaluation = &common
			// A source whose value failed generic JSON validation abstains
			// from field votes; its abstention still belongs in Faults so the
			// degraded participation stays visible.
			for _, m := range common.Malformed {
				state.aggregateFaults = append(state.aggregateFaults, fmt.Errorf("aggregate fields: %w", sourceFaultOf(m)))
			}
		} else {
			state.aggregateErr = observationErr
		}
	}
	if state.collectErr != nil && !hasUsableEmission(state.emissions) {
		decision := unknownDecision(state.capture.Reference(), state.participants, preparedRequest(state.prepared.Method, state.preparedJSON))
		state.attachFaults(&decision, nil)
		a.recordDecision(started, state.prepared.Method, state.validityPolicy(), decision, state.collectErr, state.emissions, nil)
		return &earlyExit{decision: decision, err: state.collectErr}
	}
	// Admission runs before clustering, so when every delivered response is
	// malformed there is no usable cluster from which Strict could preserve
	// the more specific malformed-response class. Keep that class visible to
	// typed callers instead of replacing it with ErrNoValidResponse.
	if state.collectErr == nil && !hasUsableEmission(state.emissions) && hasMalformedAdmission(state.emissions) {
		admissionErr := firstMalformedAdmission(state.emissions)
		decision := unknownDecision(state.capture.Reference(), state.participants, preparedRequest(state.prepared.Method, state.preparedJSON))
		state.attachFaults(&decision, nil)
		err := fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, admissionErr)
		a.recordDecision(started, state.prepared.Method, state.validityPolicy(), decision, err, state.emissions, nil)
		return &earlyExit{decision: decision, err: err}
	}
	return nil
}

// resolveValue resolves the decision through the read's resolution entry —
// the plan's declared semantics or the policy's capability chain — then
// applies the corroboration-required gate. The requirement rides the
// invocation (AggregateStrict); a read that claims agreement must resolve
// Corroborated or fail closed.
func (state *invocationState) resolveValue() (rpc.Decision, error) {
	res := state.resolve()
	decision, resolveErr := res.decision, res.err
	state.verdicts = finalizeVerdicts(res.verdicts, state.emissions)
	if resolveErr == nil && state.snapOK && state.snap.Mode == rpc.CorroborationForwarded {
		decision.Status = rpc.Forwarded
	}
	requiresAgreement := state.requiresAgreement()
	// All named conditions are pure queries, so hoisting them out of the
	// short-circuit chain below cannot change behavior; the remaining checks
	// keep their original order.
	gatedByForwarded := state.snapOK && state.snap.Mode == rpc.CorroborationForwarded
	pendingLocal := state.pendingLocalRoute()
	if requiresAgreement && state.invocation.RequireCorroboration() && !gatedByForwarded && !pendingLocal && resolveErr == nil && decision.Status != rpc.Corroborated {
		// Fail closed without rewriting the formed Status: the invocation
		// resolved, so the decision keeps the status its resolution earned
		// (typically Selected) and the unmet requirement surfaces as the
		// call error — the exact "failed call may still carry a non-Unknown
		// Status" case the Decision contract reserves for post-resolution
		// failures. Rewriting to Unknown here would break the no-rewrite
		// rule and pair Unknown with a formed Value and Support.
		resolveErr = fmt.Errorf("typed read requires corroboration: %w", rpc.ErrConflict)
	}
	return decision, resolveErr
}

// projectPhase finishes the decision: reference, participants, prepared
// request, diagnostics, semantic projection, block pin, and the fault lists.
func (a *Aggregator) projectPhase(state *invocationState, started time.Time, req rpc.Request, decision rpc.Decision, resolveErr error) (rpc.Decision, error) {
	decision.Reference = state.capture.Reference()
	decision.Participants = state.participants
	decision.Prepared = preparedRequest(state.prepared.Method, state.preparedJSON)
	if resolveErr == nil && state.evaluation != nil {
		var (
			projector rpc.ObservationProjector
			ok        bool
		)
		// The projection resolutions are separate per route: a planned read
		// looks its projector up by the plan's declared projector ID — the
		// business identity the runtime binds, which may differ from the
		// method name and may be shared with other reads — then falls back
		// to the resolver's own projection capability; the legacy read
		// keeps the method-name binding plus the chained policy's
		// capability. A read with no projector ID at all stays
		// unprojected; neither route falls back to the method name on the
		// plan path.
		if state.plan.ProjectorID != "" {
			projector, ok = a.projectors[state.plan.ProjectorID]
		}
		if !ok {
			projector, ok = state.plan.Resolver.(rpc.ObservationProjector)
		}
		if ok {
			if extensions, projectionErr := projector.ProjectObservation(decision.Value, *state.evaluation); projectionErr == nil {
				decision.Extensions = &extensions
			} else {
				decision.Faults = append(decision.Faults, fmt.Errorf("projection: %w", projectionErr))
			}
		}
	}
	// The plan records its pin at rewrite time.
	decision.BlockRef = state.planPin
	state.attachFaults(&decision, state.verdicts)
	a.recordDecision(started, req.Method, state.validityPolicy(), decision, resolveErr, state.emissions, state.verdicts)
	return decision, resolveErr
}

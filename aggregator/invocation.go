package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

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
	// reference is the evidence the establishment stage transferred for this
	// invocation: non-nil whenever the stage ran and formed anything, whether
	// the establishment succeeded or failed, nil when no round formed
	// anything. The decision's reference attaches a private copy of this
	// value, so the transferred evidence and every delivered decision stay
	// independently mutable.
	reference *rpc.ReferenceEvidence
	// collector is this invocation's material capture, resolved once at
	// entry. It is nil when no evidence configuration applies; the per-source
	// stages consult it instead of re-resolving the context.
	collector *uncertain.Capture

	// preparedMethod is the real method of the prepared request. Preparation
	// must preserve the requested method, and every wire call, record, and
	// event reads this frozen string — never a plan field or the caller's
	// request. The prepared request's Go parameter values stay inside
	// planPreparePhase: they serve the plan's own prepare-time queries
	// (pending shape, reference requirement) and are dead afterwards.
	preparedMethod string
	// wireParams is the frozen parameter list the business fan-out sends:
	// the one controlled serialization from prepare, so the wire and the
	// recorded prepared-request fact stay the same bytes. preparedJSON is
	// the joined array snapshot carried on Decision.Prepared.
	wireParams   []any
	preparedJSON json.RawMessage

	// admissionRequest is the shared read-only semantic JSON view of the
	// frozen params, decoded once from the array snapshot.
	admissionRequest rpc.Request

	// pendingLocal is the invocation's frozen pending-local route: a pending
	// request shape on a pending-observation read resolves as a node-local
	// observation, except in Forwarded mode, where the pending shape still
	// forwards. Computed once from the prepared request at prepare time;
	// resolution and the processor-route decision read this fact instead of
	// re-reading the prepared parameters.
	pendingLocal bool
	// freshness is the business round's frozen tip-freshness scope, computed
	// once from the prepared request at prepare time: a rewritten pin and a
	// request that never carried a moving tag are exempt. Collection reads
	// this fact instead of re-deriving it from the parameters.
	freshness pool.TipFreshnessScope

	// processorRoute is the invocation's frozen processing route: non-nil
	// when this round resolves through the plan's canonical processor. The
	// decision is made once at prepare time — pending-local shapes and
	// forwarded modes keep the complete raw route — and every later phase
	// (admission, resolution, faults, events, health) consults this value
	// instead of re-deriving it.
	processorRoute rpc.CanonicalProcessor
	// canonicalResolver carries the capability checked when prepare freezes
	// the processing route. A missing capability records an internal fault,
	// so the round never reaches this resolver.
	canonicalResolver rpc.CanonicalOutcomeResolver
	// canonicalObservations holds the per-source slot pairing for the
	// canonical route: each attempted source keeps its slot, failed ones
	// without a result.
	canonicalObservations []rpc.CanonicalObservation
	// internalFault records an internal contract failure of the canonical
	// seam (rpc.ErrInvalidCanonicalResult class). It terminates the round's
	// success regardless of quorum, stays out of the per-source fault tables,
	// and never blames a provider.
	internalFault error

	emissions  []pool.Emission
	collectErr error

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

// resolverPolicy is the policy whose validity contract classifies answers
// and attempts: the plan's resolver value. Normalization and the
// RPC-error-body opt-in live on it.
func (state *invocationState) resolverPolicy() rpc.Policy {
	if state.plan == nil {
		return nil
	}
	return state.plan.Resolver
}

// requiresAgreement reports whether the read's resolution claims a
// corroboration-bound agreement: the resolver's own capability.
func (state *invocationState) requiresAgreement() bool {
	agreement, ok := state.resolverPolicy().(rpc.AgreementPolicy)
	return ok && agreement.RequiresAgreement()
}

// pendingLocalRoute is the invocation's frozen pending-local fact: computed
// once from the prepared request at prepare time, never re-derived from the
// parameters afterwards.
func (state *invocationState) pendingLocalRoute() bool {
	return state.pendingLocal
}

// forwarded reports the invocation's frozen corroboration mode: an explicit
// Forwarded snapshot forwards regardless of the method's declaration. Every
// phase reads this one fact instead of re-evaluating the snapshot pair.
func (state *invocationState) forwarded() bool {
	return state.snapOK && state.snap.Mode == rpc.CorroborationForwarded
}

// resolve runs the plan's declared resolution semantics. The canonical route
// resolves through the same resolver's canonical consumption entries; the raw
// route keeps the planned chain, reading the invocation's frozen pending
// fact instead of the prepared parameters. The outcome is the resolution's
// one production fact: mandatory per-source verdicts travel with it;
// every input has a judgment, including unassessed inputs on early exits.
func (state *invocationState) resolve() (rpc.Outcome, error) {
	if state.processorRoute != nil {
		if state.snapOK && state.plan.Corroborating() {
			return state.canonicalResolver.ResolveCanonicalCorroborated(state.snap, state.canonicalObservations)
		}
		return state.canonicalResolver.ResolveCanonical(state.canonicalObservations)
	}
	return resolvePlannedInvocation(state.plan, state.snap, state.snapOK, state.pendingLocal, state.observations)
}

// establishedReference returns a private copy of the evidence the
// establishment formed, or nil when no establishment ran or formed anything.
// The copy is what every decision attaches, so the transferred evidence and
// the delivered decisions never share mutable slices.
func (state *invocationState) establishedReference() *rpc.ReferenceEvidence {
	if state.reference == nil {
		return nil
	}
	clone := state.reference.Clone()
	return &clone
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

// collectPhase fans the prepared request out under the invocation's collection
// mode: forwarded, single-source, fixed-basis voting, or symmetric collection.
// A planned read has no single-source shape; its collection mode follows the
// declared resolver class, and the tip-freshness scope is the invocation's
// frozen fact from prepare.
func (a *Aggregator) collectPhase(state *invocationState, members []*pool.Source) {
	opts := pool.CollectOptions{
		Timeout:      a.collectTimeout,
		Sources:      members,
		TipFreshness: state.freshness,
		DeferHealth:  true,
	}
	switch {
	case state.forwarded(), singleSourcePolicy(state.resolverPolicy()):
		// The frozen mode or the resolver's single-source declaration forwards.
		state.emissions, state.collectErr = a.pool.Forward(state.ctx, state.preparedMethod, state.wireParams, opts)
	case state.snapOK && state.plan.Corroborating():
		// A corroborated read collects over the fixed voting basis — every
		// member occupies the denominator even when a scheduling gate
		// skipped its request (decisions 0025 and 0062).
		state.emissions, state.collectErr = a.pool.CollectVoting(state.ctx, state.preparedMethod, state.wireParams, opts)
	default:
		state.emissions, state.collectErr = a.pool.Collect(state.ctx, state.preparedMethod, state.wireParams, opts)
	}
	// Forwarding may leave members unvisited; a pool closed before collection
	// may return no slots at all. Preserve the same frozen membership on every
	// route, with no attempt or communication cause invented for missing slots.
	if len(state.emissions) < len(members) {
		seen := make(map[string]bool, len(state.emissions))
		for _, emission := range state.emissions {
			seen[emission.Src.ID()] = true
		}
		for _, member := range members {
			if !seen[member.ID()] {
				state.emissions = append(state.emissions, pool.Emission{Src: member})
			}
		}
	}

}

// finalizeHealth consumes the same recorded judgments as public evidence and
// events. Internal processing failures never assign provider blame.
func (state *invocationState) finalizeHealth() {
	verdicts := verdictIndexByID(state.verdicts)
	for _, emission := range state.emissions {
		v, ok := verdicts[SourceRefOf(emission.Src).ID]
		if !ok || v.Method.State == rpc.InterpretationInternalFailure {
			emission.FinalizeHealth(nil)
			continue
		}
		emission.FinalizeHealth(v.Cause(emission.Response))
	}
}

func singleSourcePolicy(policy rpc.Policy) bool {
	single, ok := policy.(rpc.SingleSourcePolicy)
	return ok && single.SingleSource()
}

// runPerSourceStage runs one frozen per-source stage concurrently over the
// invocation's delivered slots. Both routes share the same skip rule, the
// same concurrency shape, the same source slots, and the same same-source
// caller; the stage callback owns the route-specific outcome handling.
func (a *Aggregator) runPerSourceStage(state *invocationState, stage func(i int)) {
	var wg sync.WaitGroup
	for i := range state.emissions {
		if state.emissions[i].Response.Err() != nil || state.emissions[i].Response.Answer.Result == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			stage(i)
		}()
	}
	wg.Wait()
}

// recordAdmissionFault writes one per-source stage failure to the raw
// material trail and gives the source the admission-stage attribution both
// routes produce.
func (state *invocationState) recordAdmissionFault(i int, cause error) {
	if state.collector != nil {
		state.collector.Record("admission", state.preparedMethod, SourceRefOf(state.emissions[i].Src), nil, cause.Error())
	}
	judgment := rpc.InterpretationRejected
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		judgment = rpc.InterpretationUnassessed
	}
	state.emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: judgment, Cause: fmt.Errorf("admission: %w", cause)}
}

// admitPhase preserves the raw responses, rejects structurally unsafe JSON,
// and runs the invocation's frozen per-source stage. On the raw route that is
// the method's admission hook with a same-source caller; on the canonical
// route it is the plan's processor. The stage runs before normalization and
// clustering: a malformed body cannot win a cluster, and dependent content is
// always fetched from the source that supplied the primary response. Both
// routes keep the same concurrency, source slots, same-source caller,
// deadline, Close, and dependency-capture semantics.
func (a *Aggregator) admitPhase(state *invocationState) {
	// Capture every primary response before admission records its judgment.
	// Uncertain keeps these raw bytes with admission and resolver outcomes
	// recorded as separate stages below.
	if state.collector != nil {
		for _, emission := range state.emissions {
			source := SourceRefOf(emission.Src)
			errText := ""
			if emission.Response.Err() != nil {
				errText = emission.Response.Err().Error()
			}
			state.collector.Record("response", state.preparedMethod, source, emission.Response.Answer.Result, errText)
		}
		if state.collectErr != nil {
			state.collector.Record("collect", state.preparedMethod, rpc.SourceRef{}, nil, state.collectErr.Error())
		}
	}
	RejectUnsafeJSON(state.emissions)
	if state.processorRoute != nil {
		// A route frozen onto a composition already found broken never
		// processes: the internal fault is set and the resolution exit
		// reports it, and the raw admission hook below stays out of reach.
		if state.internalFault == nil {
			a.processPhase(state)
		}
		return
	}
	admission := state.plan.Admit
	if admission == nil {
		return
	}
	a.runPerSourceStage(state, func(i int) {
		evidence, admissionErr := admission(state.ctx, state.admissionRequest, state.emissions[i].Response.Answer.Result, a.sameSourceCaller(state, i))
		if admissionErr != nil {
			state.recordAdmissionFault(i, admissionErr)
			return
		}
		state.emissions[i].Evidence = evidence
		state.emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationAdmitted}
	})
}

// pairCanonicalObservation pairs one slot's observation with the processed
// result its stage produced and writes the paired value to that slot's
// canonical observation. A pairing refusal is the internal contract fault of
// the slot — recorded beside the slot's other internal errors, never against
// the provider. The caller is the slot's one writer.
func (state *invocationState) pairCanonicalObservation(internal []error, i int, result rpc.CanonicalResult) {
	observation := ObservationFromEmission(state.emissions[i])
	paired, err := rpc.NewCanonicalObservation(observation, result)
	if err == nil {
		state.canonicalObservations[i] = paired
		return
	}
	internal[i] = fmt.Errorf("%w: %s", rpc.ErrInvalidCanonicalResult, err.Error())
	state.emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationInternalFailure, Cause: internal[i]}
}

// processPhase is the canonical route's per-source stage: each available
// source's response is processed exactly once by the frozen processor, and
// the result lands back in that source's own slot. Every slot is written by
// exactly one writer — the launch loop pairs a slot whose stage never runs,
// and an available slot's own goroutine pairs whatever its processing
// produced, observation included. A processor failure keeps the
// admission-phase attribution the raw route would have produced; an internal
// contract fault is recorded for the round, never against the source, and
// the round's internal faults join in member order after the Wait.
func (a *Aggregator) processPhase(state *invocationState) {
	state.canonicalObservations = make([]rpc.CanonicalObservation, len(state.emissions))
	internal := make([]error, len(state.emissions))
	for i := range state.emissions {
		if state.emissions[i].Response.Err() == nil && state.emissions[i].Response.Answer.Result != nil {
			continue
		}
		// A slot without an available response keeps its slot without a
		// result. Its emission cannot change afterwards — every slot is
		// written by its own writer alone — so the pairing formed here is
		// the pairing resolution reads.
		state.pairCanonicalObservation(internal, i, rpc.CanonicalResult{})
	}
	a.runPerSourceStage(state, func(i int) {
		result, err := state.processorRoute(state.ctx, state.admissionRequest, state.emissions[i].Response.Answer.Result, a.sameSourceCaller(state, i))
		switch {
		case err == nil:
			state.emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationAdmitted}
			state.pairCanonicalObservation(internal, i, result)
		case errors.Is(err, rpc.ErrInvalidCanonicalResult):
			// A contract fault is the process's, not the provider's:
			// the original answer stays intact and the method state
			// identifies the failure without blaming the source.
			internal[i] = err
			state.emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationInternalFailure, Cause: err}
		case len(result.Value()) != 0:
			// The CanonicalProcessor contract pairs outcomes: an error
			// must carry a zero result, so a non-zero result beside a
			// non-sentinel error is the same internal contract defect.
			// The pairing is verified here — before anything attributes
			// the error to the source or records an admission fault —
			// and the round takes the one internal exit.
			internal[i] = fmt.Errorf("%w: processor returned a canonical result beside an error", rpc.ErrInvalidCanonicalResult)
			state.emissions[i].Response.Answer.Interpretation = rpc.Interpretation{State: rpc.InterpretationInternalFailure, Cause: internal[i]}
		default:
			state.recordAdmissionFault(i, err)
			// The failed source keeps its slot without a result; the
			// observation carries the admission fault the pairing reads.
			state.pairCanonicalObservation(internal, i, rpc.CanonicalResult{})
		}
	})
	for _, err := range internal {
		state.internalFault = errors.Join(state.internalFault, err)
	}
}

// sameSourceCaller binds one emission's slot to the same-source dependency
// caller both routes share: the call goes to the source that supplied the
// primary response, and the raw material is recorded on the invocation's
// capture.
func (a *Aggregator) sameSourceCaller(state *invocationState, i int) rpc.AdmissionCaller {
	return func(callCtx context.Context, method string, params []any) (json.RawMessage, error) {
		raw, callErr := a.pool.CallSource(callCtx, state.emissions[i].Src, method, params)
		if state.collector != nil {
			source := SourceRefOf(state.emissions[i].Src)
			errText := ""
			if callErr != nil {
				errText = callErr.Error()
			}
			state.collector.Record("dependency", method, source, raw, errText)
		}
		return raw, callErr
	}
}

// resolvePhase assembles observations, aggregates fields, and adjudicates.
// A non-nil early exit reports the two degraded exits (collection failure
// with nothing usable, and every-delivered-response-malformed) plus the
// canonical route's internal-contract abort; otherwise the state carries the
// resolution outcome.
func (a *Aggregator) resolvePhase(state *invocationState, started time.Time) *earlyExit {
	// The canonical route's slots hold the observation each pairing formed
	// after processing; when the route completed without an internal fault
	// every slot is paired, so the raw field aggregation and the resolution
	// read that same formed value instead of rebuilding it from the
	// emission. The raw route has no pairing and forms its observations
	// here, from the emissions.
	readsCanonicalPairing := state.processorRoute != nil && state.internalFault == nil
	state.observations = make([]rpc.Observation, len(state.emissions))
	state.participants = make([]rpc.SourceRef, 0, len(state.emissions))
	for i, emission := range state.emissions {
		if readsCanonicalPairing {
			state.observations[i] = state.canonicalObservations[i].Observation()
		} else {
			state.observations[i] = ObservationFromEmission(emission)
		}
		state.participants = append(state.participants, state.observations[i].Source)
	}
	// The canonical seam's internal contract fault terminates the round's
	// success before any resolution, even with quorum in reach. The decision
	// stays Unknown; the fault is named once on the decision outside the
	// per-source tables; the per-source statement comes from the invocation's
	// own records, so no raw hook re-runs to classify anyone; the other known
	// transport faults keep their attribution, and the internal fault itself
	// stays provider-neutral. The canonical fault joins the decision before
	// the observability event folds it, so the event's fault count equals the
	// returned decision's.
	if state.internalFault != nil {
		outcome := RoundOutcome(state.emissions)
		state.verdicts = outcome.Verdicts
		decision := unknownDecision(state.establishedReference(), state.participants, preparedRequest(state.preparedMethod, state.preparedJSON))
		decision.Sources, decision.SourceFaults, decision.Faults = outcome.Decision.Sources, outcome.Decision.SourceFaults, outcome.Decision.Faults
		decision.Adjudication = rpc.Adjudication{Basis: state.invocation.Basis().Size(), Required: state.invocation.Basis().Required(), Reason: rpc.ReasonInternalFailure}
		state.attachFaults(&decision)
		decision.Faults = append(decision.Faults, fmt.Errorf("canonical: %w", state.internalFault))
		a.recordDecision(started, state.preparedMethod, decision, state.internalFault, state.emissions, state.verdicts)
		return &earlyExit{decision: decision, err: state.internalFault}
	}
	// A field-aggregation failure only degrades this invocation to a decision
	// without diagnostics; the error itself is kept so every return path
	// below can attach it to Faults instead of dropping it silently.
	if state.snapOK && !state.forwarded() {
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
		return a.unknownExit(state, started, state.collectErr)
	}
	// Admission runs before clustering, so when every delivered response is
	// malformed there is no usable cluster from which Strict could preserve
	// the more specific malformed-response class. Keep that class visible to
	// typed callers instead of replacing it with ErrNoValidResponse.
	if state.collectErr == nil && !hasUsableEmission(state.emissions) && hasMalformedAdmission(state.emissions) {
		admissionErr := firstMalformedAdmission(state.emissions)
		return a.unknownExit(state, started, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, admissionErr))
	}
	return nil
}

// unknownExit assembles the shared degraded-resolution exit: the Unknown
// decision carrying the invocation's reference, participants and prepared
// fact, the shared fault-table assembly, one decision event, and the early
// exit pair. One unassessed outcome serves faults, events and health.
func (a *Aggregator) unknownExit(state *invocationState, started time.Time, resolveErr error) *earlyExit {
	decision := unknownDecision(state.establishedReference(), state.participants, preparedRequest(state.preparedMethod, state.preparedJSON))
	outcome := RoundOutcome(state.emissions)
	state.verdicts = outcome.Verdicts
	decision.Sources, decision.SourceFaults, decision.Faults = outcome.Decision.Sources, outcome.Decision.SourceFaults, outcome.Decision.Faults
	decision.Adjudication = rpc.Adjudication{Basis: state.invocation.Basis().Size(), Required: state.invocation.Basis().Required()}
	state.attachFaults(&decision)
	a.recordDecision(started, state.preparedMethod, decision, resolveErr, state.emissions, state.verdicts)
	return &earlyExit{decision: decision, err: resolveErr}
}

// resolveValue resolves the decision through the read's resolution entry —
// the plan's declared semantics or the policy's capability chain — then
// applies the corroboration-required gate. The requirement rides the
// invocation (AggregateStrict); a read that claims agreement must resolve
// Corroborated or fail closed.
func (state *invocationState) resolveValue() (rpc.Decision, error) {
	outcome, resolveErr := state.resolve()
	contractErr := outcome.Validate(state.observations)
	if contractErr == nil {
		members := state.invocation.Basis().Members
		if len(members) != len(state.observations) {
			contractErr = fmt.Errorf("resolution input differs from frozen membership: %w", rpc.ErrInvalidOutcome)
		} else {
			frozen := make(map[string]rpc.SourceRef, len(members))
			for _, member := range members {
				frozen[member.ID] = member
			}
			for _, observation := range state.observations {
				if member, exists := frozen[observation.Source.ID]; !exists || member != observation.Source {
					contractErr = fmt.Errorf("resolution input changed a frozen member: %w", rpc.ErrInvalidOutcome)
					break
				}
			}
		}
	}
	if contractErr != nil {
		state.internalFault = errors.Join(state.internalFault, contractErr)
		outcome = RoundOutcome(state.emissions)
		outcome.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
		outcome.Decision.Adjudication.Required = state.invocation.Basis().Required()
		outcome.Decision.Faults = append(outcome.Decision.Faults, contractErr)
		resolveErr = errors.Join(contractErr, resolveErr)
	}
	outcome.ProjectSources(state.observations)
	decision := outcome.Decision
	// Required belongs to the selected resolution contract. Snapshot routes
	// already supplied the frozen rule; constructor/default routes retain
	// their own threshold. Validation never repairs a claimed basis or bound.
	state.verdicts = outcome.Verdicts
	if resolveErr == nil && state.forwarded() {
		decision.Status = rpc.Forwarded
	}
	requiresAgreement := state.requiresAgreement()
	// All named conditions are pure queries, so hoisting them out of the
	// short-circuit chain below cannot change behavior; the remaining checks
	// keep their original order.
	gatedByForwarded := state.forwarded()
	pendingLocal := state.pendingLocalRoute()
	if requiresAgreement && state.invocation.RequireCorroboration() && !gatedByForwarded && !pendingLocal && resolveErr == nil && decision.Status != rpc.Corroborated {
		// Fail closed without rewriting the formed Status: the invocation
		// resolved, so the decision keeps the status its resolution earned
		// (typically Selected) and the unmet requirement surfaces as the
		// call error — the exact "failed call may still carry a non-Unknown
		// Status" case the Decision contract reserves for post-resolution
		// failures. Rewriting to Unknown here would break the no-rewrite
		// rule and pair Unknown with a formed Value and Support.
		decision.Adjudication.Reason = rpc.ReasonGuaranteeNotMet
		resolveErr = fmt.Errorf("typed read requires corroboration: %w", rpc.ErrGuaranteeNotMet)
	}
	return decision, resolveErr
}

// projectPhase finishes the decision: reference, participants, prepared
// request, diagnostics, semantic projection, block pin, and the fault lists.
// The observability event names the real prepared method, like every other
// phase — nothing reads the caller's request after preparation.
func (a *Aggregator) projectPhase(state *invocationState, started time.Time, decision rpc.Decision, resolveErr error) (rpc.Decision, error) {
	decision.Reference = state.establishedReference()
	decision.Participants = state.participants
	decision.Prepared = preparedRequest(state.preparedMethod, state.preparedJSON)
	if resolveErr == nil && state.evaluation != nil {
		var (
			projector rpc.ObservationProjector
			ok        bool
		)
		// Resolve the plan's declared projector ID first, then use the
		// resolver's projection capability when no binding was found.
		// With neither, the decision has no observation extensions.
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
	state.attachFaults(&decision)
	a.recordDecision(started, state.preparedMethod, decision, resolveErr, state.emissions, state.verdicts)
	return decision, resolveErr
}

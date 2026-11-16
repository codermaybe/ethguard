package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

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
	capture *refcontext.Capture
	// collector is this invocation's material capture, resolved once at
	// entry. It is nil when no evidence configuration applies; the per-source
	// stages consult it instead of re-resolving the context.
	collector *uncertain.Capture

	prepared rpc.Request
	// wireParams is the frozen parameter list the business fan-out sends:
	// the one controlled serialization from prepare, so the wire and the
	// recorded prepared-request fact stay the same bytes. preparedJSON is
	// the joined array snapshot carried on Decision.Prepared.
	wireParams   []any
	preparedJSON json.RawMessage

	// admissionRequest is the shared read-only semantic JSON view of the
	// frozen params. prepared retains Go values for plan/reference queries.
	admissionRequest rpc.Request

	// processorRoute is the invocation's frozen processing route: non-nil
	// when this round resolves through the plan's canonical processor. The
	// decision is made once at prepare time — pending-local shapes and
	// forwarded modes keep the complete raw route — and every later phase
	// (admission, resolution, faults, events, health) consults this value
	// instead of re-deriving it.
	processorRoute rpc.CanonicalProcessor
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

// forwarded reports the invocation's frozen corroboration mode: an explicit
// Forwarded snapshot forwards regardless of the method's declaration. Every
// phase reads this one fact instead of re-evaluating the snapshot pair.
func (state *invocationState) forwarded() bool {
	return state.snapOK && state.snap.Mode == rpc.CorroborationForwarded
}

// resolve runs the plan's declared resolution semantics. The canonical route
// resolves through the same resolver's canonical consumption entries; the raw
// route keeps the planned chain.
func (state *invocationState) resolve() invocationResolution {
	if state.processorRoute != nil {
		return resolveCanonicalInvocation(state.plan, state.snap, state.snapOK, state.canonicalObservations)
	}
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
func (a *Aggregator) collectPhase(state *invocationState, members []*pool.Source) {
	opts := pool.CollectOptions{
		Timeout:      a.collectTimeout,
		Sources:      members,
		TipFreshness: preparedTipFreshness(state),
		DeferHealth:  true,
	}
	forward := func() {
		state.emissions, state.collectErr = a.pool.Forward(state.ctx, state.prepared.Method, state.wireParams, opts)
	}
	switch {
	case state.forwarded():
		// Intentional single-source forwarding: the configured mode, not the
		// method's declaration, decides.
		forward()
	case singleSourcePolicy(state.plan.Resolver):
		// The explicit forward plan (or a single-source declaration).
		forward()
	case state.snapOK && state.plan.Corroborating():
		// A corroborated read collects over the fixed voting basis — every
		// member occupies the denominator even when a scheduling gate
		// skipped its request (decisions 0025 and 0062).
		state.emissions, state.collectErr = a.pool.CollectVoting(state.ctx, state.prepared.Method, state.wireParams, opts)
	default:
		state.emissions, state.collectErr = a.pool.Collect(state.ctx, state.prepared.Method, state.wireParams, opts)
	}
}

// finalizeHealth runs on every invocation exit. Admission errors and resolver
// verdicts are source-bound; an overall decision error cannot assign blame.
// Resolvers that omit verdicts on early exits (including all-EncodeCheck
// failure in Strict) fall back to the existing per-source validation checks.
// The fallback re-runs the raw validity hooks, so it exists on the raw route
// only: a canonical-route invocation either has the resolution's own
// statement or aborted on an internal contract fault, and in both cases its
// successful sources must not be re-parsed or penalized here.
func (state *invocationState) finalizeHealth() {
	verdicts := verdictIndexByID(state.verdicts)
	for _, emission := range state.emissions {
		responseErr := emission.Err
		if !errors.Is(responseErr, rpc.ErrMalformedResponse) && emission.Src != nil {
			if verdict, ok := verdicts[emission.Src.ID()]; ok && verdict.Kind == rpc.VerdictUnusable {
				responseErr = verdict.Err
			} else if state.processorRoute == nil && state.verdicts == nil && emission.Err == nil && emission.Val != nil {
				_, responseErr = rpc.CanonicalOf(state.validityPolicy(), emission.Val)
				if responseErr == nil || errors.Is(responseErr, rpc.ErrNotCanonicalScalar) {
					_, responseErr = state.validityPolicy().Normalize(emission.Val)
				}
			}
		}
		emission.FinalizeHealth(responseErr)
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
		if state.emissions[i].Err != nil || state.emissions[i].Val == nil {
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
		state.collector.Record("admission", state.prepared.Method, SourceRefOf(state.emissions[i].Src), nil, cause.Error())
	}
	state.emissions[i].Err = fmt.Errorf("admission: %w", cause)
	state.emissions[i].Val = nil
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
	// Preserve every primary response before admission is allowed to clear a
	// malformed value. This is the raw per-source record used by Uncertain;
	// admission and resolver outcomes are recorded as separate stages below.
	if state.collector != nil {
		for _, emission := range state.emissions {
			source := SourceRefOf(emission.Src)
			errText := ""
			if emission.Err != nil {
				errText = emission.Err.Error()
			}
			state.collector.Record("response", state.prepared.Method, source, emission.Val, errText)
		}
		if state.collectErr != nil {
			state.collector.Record("collect", state.prepared.Method, rpc.SourceRef{}, nil, state.collectErr.Error())
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
	admission, ok := state.admissionHook()
	if !ok {
		return
	}
	a.runPerSourceStage(state, func(i int) {
		evidence, admissionErr := admission(state.ctx, state.admissionRequest, state.emissions[i].Val, a.sameSourceCaller(state, i))
		if admissionErr != nil {
			state.recordAdmissionFault(i, admissionErr)
			return
		}
		state.emissions[i].Evidence = evidence
	})
}

// processPhase is the canonical route's per-source stage: each available
// source's response is processed exactly once by the frozen processor, and
// the result lands back in that source's own slot. A processor failure keeps
// the admission-phase attribution the raw route would have produced; an
// internal contract fault is recorded for the round, never against the
// source.
func (a *Aggregator) processPhase(state *invocationState) {
	state.canonicalObservations = make([]rpc.CanonicalObservation, len(state.emissions))
	results := make([]rpc.CanonicalResult, len(state.emissions))
	internal := make([]error, len(state.emissions))
	a.runPerSourceStage(state, func(i int) {
		result, err := state.processorRoute(state.ctx, state.admissionRequest, state.emissions[i].Val, a.sameSourceCaller(state, i))
		if err == nil {
			results[i] = result
			return
		}
		if errors.Is(err, rpc.ErrInvalidCanonicalResult) {
			// A contract fault is the process's, not the provider's:
			// the emission stays untouched so no fault or health table
			// blames the source.
			internal[i] = err
			return
		}
		// The CanonicalProcessor contract pairs outcomes: an error
		// must carry a zero result, so a non-zero result beside a
		// non-sentinel error is the same internal contract defect.
		// The pairing is verified here — before anything attributes
		// the error to the source or records an admission fault —
		// and the round takes the one internal exit.
		if len(result.Value()) != 0 {
			internal[i] = fmt.Errorf("%w: processor returned a canonical result beside an error", rpc.ErrInvalidCanonicalResult)
			return
		}
		state.recordAdmissionFault(i, err)
	})
	for i := range state.emissions {
		if internal[i] != nil {
			state.internalFault = errors.Join(state.internalFault, internal[i])
			continue
		}
		observation := ObservationFromEmission(state.emissions[i])
		if observation.Err != nil {
			// A failed source keeps its slot without a result.
			paired, err := rpc.NewCanonicalObservation(observation, rpc.CanonicalResult{})
			if err != nil {
				state.internalFault = errors.Join(state.internalFault, err)
				continue
			}
			state.canonicalObservations[i] = paired
			continue
		}
		paired, err := rpc.NewCanonicalObservation(observation, results[i])
		if err != nil {
			// The processor returned no result beside a nil error: the
			// internal contract sentinel, never a provider verdict.
			state.internalFault = errors.Join(state.internalFault, fmt.Errorf("%w: %s", rpc.ErrInvalidCanonicalResult, err.Error()))
			continue
		}
		state.canonicalObservations[i] = paired
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
	state.observations = make([]rpc.Observation, len(state.emissions))
	state.participants = make([]rpc.SourceRef, 0, len(state.emissions))
	for i, emission := range state.emissions {
		state.observations[i] = ObservationFromEmission(emission)
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
		state.verdicts = finalizeVerdicts(verdictsFromSlots(state.emissions), state.emissions)
		decision := unknownDecision(state.capture.Reference(), state.participants, preparedRequest(state.prepared.Method, state.preparedJSON))
		state.attachFaults(&decision, state.verdicts)
		decision.Faults = append(decision.Faults, fmt.Errorf("canonical: %w", state.internalFault))
		a.recordDecision(started, state.prepared.Method, state.validityPolicy(), decision, state.internalFault, state.emissions, state.verdicts)
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
// exit pair. Degraded exits reach this with a nil verdict statement, so the
// fault tables classify from the invocation's own records.
func (a *Aggregator) unknownExit(state *invocationState, started time.Time, resolveErr error) *earlyExit {
	decision := unknownDecision(state.capture.Reference(), state.participants, preparedRequest(state.prepared.Method, state.preparedJSON))
	state.attachFaults(&decision, state.verdicts)
	a.recordDecision(started, state.prepared.Method, state.validityPolicy(), decision, resolveErr, state.emissions, state.verdicts)
	return &earlyExit{decision: decision, err: resolveErr}
}

// verdictsFromSlots states every source's outcome from the invocation's own
// records: a delivered error is unusable with its own error, a delivered
// value counts as supported. Supported here is the attempt statement — the
// exchange succeeded and the value was usable — not a claim the round
// adjudicated: on a degraded exit the resolution never classified anyone and
// no raw hook may run to do it afterwards. The verdicts keep the fault tables
// and the event classification from re-judging the slots by a second
// classification pass.

func verdictsFromSlots(emissions []pool.Emission) []rpc.SourceVerdict {
	verdicts := make([]rpc.SourceVerdict, 0, len(emissions))
	for _, emission := range emissions {
		verdict := rpc.SourceVerdict{Source: SourceRefOf(emission.Src), Kind: rpc.VerdictSupported}
		if emission.Err != nil {
			verdict.Kind = rpc.VerdictUnusable
			verdict.Err = emission.Err
		}
		verdicts = append(verdicts, verdict)
	}
	sort.Slice(verdicts, func(i, j int) bool {
		if verdicts[i].Source.ID != verdicts[j].Source.ID {
			return verdicts[i].Source.ID < verdicts[j].Source.ID
		}
		return verdicts[i].Source.Name < verdicts[j].Source.Name
	})
	return verdicts
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

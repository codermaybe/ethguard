package aggregator

import (
	"context"
	"encoding/json"
	"errors"
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
// driven by an explicit execution plan carries the plan instead of a
// compiled policy: the plan path reads its routing decisions from the plan
// value, the legacy path type-asserts the policy's optional capabilities.
type invocationState struct {
	ctx        context.Context
	invocation rpc.Invocation
	policy     rpc.Policy
	// plan is the explicit execution plan of a planned read; nil selects the
	// legacy policy path everywhere the two routes diverge.
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

	prepared   rpc.Request
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
// and attempts: the plan's resolver value for a planned read, the whole
// compiled chain otherwise. Normalization and the RPC-error-body opt-in live
// on the resolver in both shapes.
func (state *invocationState) validityPolicy() rpc.Policy {
	if state.plan != nil {
		return state.plan.Resolver
	}
	return state.policy
}

// admissionHook is the method-owned admission hook, or absent when the read
// declares none.
func (state *invocationState) admissionHook() (func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error), bool) {
	if state.plan != nil {
		return plannedAdmissionHook(state.plan)
	}
	if admission, ok := state.policy.(rpc.AdmissionPolicy); ok {
		return admission.Admit, true
	}
	return nil, false
}

// requiresAgreement reports whether the read's resolution claims a
// corroboration-bound agreement: the resolver's own capability for a planned
// read, the chain's otherwise.
func (state *invocationState) requiresAgreement() bool {
	agreement, ok := state.validityPolicy().(rpc.AgreementPolicy)
	return ok && agreement.RequiresAgreement()
}

// pendingLocalRoute is the single pending-local routing check for both
// paths: a pending request on a pending-observation read resolves as a
// node-local observation, except in Forwarded mode, where the pending shape
// still forwards.
func (state *invocationState) pendingLocalRoute() bool {
	if state.plan != nil {
		return planPendingLocalRoute(state.plan, state.prepared, state.snap, state.snapOK)
	}
	return isPendingLocalRoute(state.policy, state.prepared, state.snap, state.snapOK)
}

// resolve runs the resolution entry the read's route selects.
func (state *invocationState) resolve() invocationResolution {
	if state.plan != nil {
		return resolvePlannedInvocation(state.plan, state.prepared, state.snap, state.snapOK, state.observations)
	}
	return resolveWithPending(state.policy, state.prepared, state.snap, state.snapOK, state.observations)
}

// earlyExit carries one degraded phase result: a classified decision plus the
// error that explains why no resolved value was reached.
type earlyExit struct {
	decision rpc.Decision
	err      error
}

// uncertainCaptureAdapter routes the invocation's raw-data records into the
// Uncertain collector. The adapter is the rpc-owned InvocationCapture seam's
// production binding for aggregator-driven reads.
type uncertainCaptureAdapter struct{ capture *uncertain.Capture }

func (a uncertainCaptureAdapter) Record(stage, method string, source rpc.SourceRef, value []byte, errText string) {
	if a.capture != nil {
		a.capture.Record(stage, method, source, value, errText)
	}
}

// beginInvocation freezes the read invocation: one ID, the voting basis
// captured before any request, the corroboration snapshot, and the raw-data
// capture. The invocation rides the context as the transitional carrier until
// every establishment path takes it explicitly.
func beginInvocation(ctx context.Context, snap rpc.Corroboration, snapOK bool, members []*pool.Source, capture *uncertain.Capture) (context.Context, rpc.Invocation) {
	refs := make([]rpc.SourceRef, 0, len(members))
	for _, member := range members {
		refs = append(refs, SourceRefOf(member))
	}
	opts := []rpc.InvocationOption{rpc.WithInvocationCapture(uncertainCaptureAdapter{capture: capture})}
	if refcontext.CorroborationRequired(ctx) {
		opts = append(opts, rpc.WithRequiredCorroboration())
	}
	invocation := rpc.NewInvocation(rpc.NewInvocationID(), rpc.FreezeBasis(refs, snap), opts...)
	return refcontext.WithInvocation(ctx, invocation), invocation
}

// harvestReference moves establishment evidence from the transitional capture
// into the invocation, so the completed value carries the reference the
// reference stage recorded during Prepare. It runs even when Prepare failed:
// a failed establishment's evidence is part of the decision.
func harvestReference(ctx context.Context, invocation rpc.Invocation, capture *refcontext.Capture) (context.Context, rpc.Invocation) {
	if capture == nil {
		return ctx, invocation
	}
	if ref := capture.Reference(); ref != nil {
		if completed, err := invocation.WithReference(*ref); err == nil {
			return refcontext.WithInvocation(ctx, completed), completed
		}
	}
	return ctx, invocation
}

// prepareWithReference is the Prepare and ReferenceStage phases. A preparing
// decorator that needs its reference reports the pipeline sentinel; the
// aggregator answers it by running the declared establishment on this same
// invocation and preparing again. Validation-before-anchor stays intact: the
// first Prepare runs every outer validator before the anchor layer asks for
// its reference. A non-nil early exit reports the Prepare or establishment
// failure decision.
func (a *Aggregator) prepareWithReference(state *invocationState, started time.Time, req rpc.Request) *earlyExit {
	prepared, err := state.policy.Prepare(state.ctx, req)
	if errors.Is(err, rpc.ErrReferenceRequired) {
		if exit := a.establishReferencePhase(state, started, req); exit != nil {
			return exit
		}
		prepared, err = state.policy.Prepare(state.ctx, req)
	}
	if ref := state.capture.Reference(); ref != nil {
		state.ctx, state.invocation = harvestReference(state.ctx, state.invocation, state.capture)
	}
	if err == nil {
		state.prepared = prepared
		return nil
	}
	if c := uncertain.FromContext(state.ctx); c != nil {
		c.Record("prepare", req.Method, rpc.SourceRef{}, nil, err.Error())
	}
	decision := rpc.Decision{Status: rpc.Unknown, Reference: state.capture.Reference()}
	a.recordDecision(started, req.Method, state.policy, decision, err, nil, nil)
	return &earlyExit{decision: decision, err: err}
}

// establishReferencePhase runs the declared establishment once per
// invocation: the policy declares which reference level it needs, the stage
// performs the network rounds, and the evidence lands on the invocation and
// the transitional capture. A nil return means the invocation now carries
// its reference and Prepare can run again.
func (a *Aggregator) establishReferencePhase(state *invocationState, started time.Time, req rpc.Request) *earlyExit {
	if a.stage == nil {
		// No establishment seam is installed: the declared reference cannot
		// be established, so the read fails before any fan-out.
		err := fmt.Errorf("reference: no establishment stage for a read that requires one: %w", rpc.ErrReferenceRequired)
		decision := rpc.Decision{Status: rpc.Unknown, Reference: state.capture.Reference()}
		a.recordDecision(started, req.Method, state.policy, decision, err, nil, nil)
		return &earlyExit{decision: decision, err: err}
	}
	request := rpc.ReferenceRequest{Kind: rpc.ReferenceLatest}
	if requirement, ok := state.policy.(rpc.ReferenceRequirement); ok {
		if declared, needed := requirement.ReferenceFor(req); needed {
			request = declared
		}
	}
	completed, err := a.stage.Establish(state.ctx, state.invocation, request)
	if err != nil {
		// A failed establishment keeps its evidence on the capture (the
		// stage publishes it); the business fan-out never starts. The error
		// carries the same "anchor:" prefix the preparing decorator used to
		// wrap its establishment cause with.
		err = fmt.Errorf("anchor: %w", err)
		decision := rpc.Decision{Status: rpc.Unknown, Reference: state.capture.Reference()}
		a.recordDecision(started, req.Method, state.policy, decision, err, nil, nil)
		return &earlyExit{decision: decision, err: err}
	}
	if ref, ok := completed.Reference(); ok {
		// The transitional capture stays the decision's evidence source.
		refcontext.Publish(state.ctx, ref)
	}
	state.ctx, state.invocation = refcontext.WithInvocation(state.ctx, completed), completed
	return nil
}

// collectPhase fans the prepared request out under the invocation's collection
// mode: forwarded, single-source, fixed-basis voting, or symmetric collection.
// A planned read has no single-source shape; its collection mode follows the
// declared resolver class.
func (a *Aggregator) collectPhase(state *invocationState) {
	opts := pool.CollectOptions{Timeout: a.collectTimeout, Sources: state.members}
	forward := func() {
		state.emissions, state.collectErr = a.pool.Forward(state.ctx, state.prepared.Method, state.prepared.Params, opts)
	}
	switch {
	case state.snapOK && state.snap.Mode == rpc.CorroborationForwarded:
		forward()
	case state.plan != nil:
		if state.corroborating {
			state.emissions, state.collectErr = a.pool.CollectVoting(state.ctx, state.prepared.Method, state.prepared.Params, opts)
		} else {
			state.emissions, state.collectErr = a.pool.Collect(state.ctx, state.prepared.Method, state.prepared.Params, opts)
		}
	case singleSourcePolicy(state.policy):
		forward()
	case state.corroborating:
		state.emissions, state.collectErr = a.pool.CollectVoting(state.ctx, state.prepared.Method, state.prepared.Params, opts)
	default:
		state.emissions, state.collectErr = a.pool.Collect(state.ctx, state.prepared.Method, state.prepared.Params, opts)
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
	if c := uncertain.FromContext(state.ctx); c != nil {
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
				if c := uncertain.FromContext(state.ctx); c != nil {
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
				if c := uncertain.FromContext(state.ctx); c != nil {
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
		common, observationErr := rpc.AggregateFieldsWithCorroboration(state.observations, state.snap)
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
		decision := unknownDecision(state.capture.Reference(), state.participants, preparedRequest(state.prepared))
		decision.Faults = collectionFaults(state.validityPolicy(), state.collectErr, state.emissions, nil)
		decision.SourceFaults = structuredSourceFaults(state.validityPolicy(), state.emissions, nil)
		decision.CollectionFaults = collectionFaultList(state.collectErr)
		state.attachFieldAggregateFaults(&decision)
		a.recordDecision(started, state.prepared.Method, state.validityPolicy(), decision, state.collectErr, state.emissions, nil)
		return &earlyExit{decision: decision, err: state.collectErr}
	}
	// Admission runs before clustering, so when every delivered response is
	// malformed there is no usable cluster from which Strict could preserve
	// the more specific malformed-response class. Keep that class visible to
	// typed callers instead of replacing it with ErrNoValidResponse.
	if state.collectErr == nil && !hasUsableEmission(state.emissions) && hasMalformedAdmission(state.emissions) {
		admissionErr := firstMalformedAdmission(state.emissions)
		decision := unknownDecision(state.capture.Reference(), state.participants, preparedRequest(state.prepared))
		decision.Faults = sourceFaults(state.validityPolicy(), state.emissions, nil)
		decision.SourceFaults = structuredSourceFaults(state.validityPolicy(), state.emissions, nil)
		state.attachFieldAggregateFaults(&decision)
		err := fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, admissionErr)
		a.recordDecision(started, state.prepared.Method, state.validityPolicy(), decision, err, state.emissions, nil)
		return &earlyExit{decision: decision, err: err}
	}
	return nil
}

// resolveValue resolves the decision through the read's resolution entry —
// the plan's declared semantics or the policy's capability chain — then
// applies the corroboration-required gate.
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
	if requiresAgreement && refcontext.CorroborationRequired(state.ctx) && !gatedByForwarded && !pendingLocal && resolveErr == nil && decision.Status != rpc.Corroborated {
		decision.Status = rpc.Unknown
		resolveErr = fmt.Errorf("typed read requires corroboration: %w", rpc.ErrConflict)
	}
	return decision, resolveErr
}

// projectPhase finishes the decision: reference, participants, prepared
// request, diagnostics, semantic projection, block pin, and the fault lists.
func (a *Aggregator) projectPhase(state *invocationState, started time.Time, req rpc.Request, decision rpc.Decision, resolveErr error) (rpc.Decision, error) {
	decision.Reference = state.capture.Reference()
	decision.Participants = state.participants
	decision.Prepared = preparedRequest(state.prepared)
	if state.evaluation != nil {
		decision.Diagnostics = &state.evaluation.Diagnostics
	}
	if resolveErr == nil && state.evaluation != nil {
		projector, ok := a.projectors[req.Method]
		if !ok {
			if state.plan != nil {
				projector, ok = state.plan.Resolver.(rpc.ObservationProjector)
			} else {
				projector, ok = state.policy.(rpc.ObservationProjector)
			}
		}
		if ok {
			if extensions, projectionErr := projector.ProjectObservation(decision.Value, *state.evaluation); projectionErr == nil {
				decision.Extensions = &extensions
			} else {
				decision.Faults = append(decision.Faults, fmt.Errorf("projection: %w", projectionErr))
			}
		}
	}
	// The plan records its pin at rewrite time; the legacy path asks the
	// chain's BlockRef providers what they rewrote.
	if state.plan != nil {
		decision.BlockRef = state.planPin
	} else if provider, ok := state.policy.(rpc.BlockRefProvider); ok {
		decision.BlockRef = provider.BlockRef(req, state.prepared)
	}
	if state.aggregateErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("aggregate fields: %w", state.aggregateErr))
	}
	decision.Faults = append(decision.Faults, state.aggregateFaults...)
	if state.collectErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("collect: %w", state.collectErr))
		decision.CollectionFaults = collectionFaultList(state.collectErr)
	}
	decision.SourceFaults = structuredSourceFaults(state.validityPolicy(), state.emissions, state.verdicts)
	decision.Faults = append(decision.Faults, sourceFaults(state.validityPolicy(), state.emissions, state.verdicts)...)
	a.recordDecision(started, req.Method, state.validityPolicy(), decision, resolveErr, state.emissions, state.verdicts)
	return decision, resolveErr
}

func (state *invocationState) attachFieldAggregateFaults(decision *rpc.Decision) {
	if state.aggregateErr != nil {
		decision.Faults = append(decision.Faults, fmt.Errorf("aggregate fields: %w", state.aggregateErr))
	}
	decision.Faults = append(decision.Faults, state.aggregateFaults...)
	if state.evaluation != nil {
		decision.Diagnostics = &state.evaluation.Diagnostics
	}
}

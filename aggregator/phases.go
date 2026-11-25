package aggregator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// The planned-read pipeline. The first four phases of the fixed phase order
// run here, driven by the plan's declared facts instead of a decorator
// chain's type assertions:
//
//	Validate / Prepare
//	→ ReferenceRequirement
//	→ ReferenceStage (when the read needs its reference)
//	→ PrepareWithReference
//
// Collect, Admit, Resolve Outcome, and Project / Observe continue through
// the shared phase implementations, which read the plan's collection mode,
// admission hook, resolution entry, and recorded pin. A local failure in any
// planned phase — validation, pending rejection, a missing establishment
// stage, a failed establishment, a rejected rewrite — fails the read before
// any business fan-out, with the same decision shape the legacy path
// reports.
//
// planPreparePhase returns a non-nil early exit when the read failed before
// producing a prepared request; otherwise the state carries the prepared
// request and the pin the rewrite recorded.
func (a *Aggregator) planPreparePhase(state *invocationState, started time.Time, req rpc.Request) *earlyExit {
	plan := state.plan
	req = plan.ApplyReferenceDefault(req)
	// Validate / Prepare: the method's own validation and its executable
	// pending rejection run before anything else and fail the read locally.
	if plan.Prepare != nil {
		prepared, err := plan.Prepare(state.ctx, req)
		if err != nil {
			return a.prepareFailureExit(state, started, req, err)
		}
		if prepared.Method != req.Method {
			return a.prepareFailureExit(state, started, req, fmt.Errorf("rpc: prepare changed method %q to %q: %w", req.Method, prepared.Method, rpc.ErrInvalidRequest))
		}
		req = prepared
	}
	if plan.PendingReject != nil {
		if err := plan.PendingReject(req); err != nil {
			return a.prepareFailureExit(state, started, req, err)
		}
	}
	// A non-empty local-only plan stays installed so its request checks cannot
	// be bypassed by the unknown-method forward fallback. Once those checks
	// pass, however, the plan is not executable: fail closed before reference
	// establishment or any provider fan-out.
	if plan.Resolver == nil {
		return a.prepareFailureExit(state, started, req, fmt.Errorf("rpc: read plan for %q has no resolver: %w", req.Method, rpc.ErrInvalidReadPlan))
	}
	// ReferenceRequirement: the declaration and the request decide whether
	// this read needs its reference; a request without a moving tag never
	// establishes.
	request, needed := plan.ReferenceRequirement(req)
	// ReferenceStage: the one establishment path. The rounds run on this
	// invocation's frozen basis; the evidence lands on the invocation and
	// the transitional capture.
	ref, refOK := rpc.ReferenceEvidence{}, false
	if needed {
		if exit := a.establishPlannedReference(state, started, req, request, &ref, &refOK); exit != nil {
			return exit
		}
	}
	// PrepareWithReference: the rewrite reads the established evidence —
	// never a network callback — and records the pin it introduced.
	prepared, pin, err := plan.PrepareWithReference(req, ref, refOK)
	if err != nil {
		return a.prepareFailureExit(state, started, req, err)
	}
	// Finalize the prepared parameters for both wire and recorded evidence.
	// A preparing hook that snapshots user values returns owned JSON, so this
	// step cannot execute those user marshalers again. Other plans retain their
	// one input serialization here. Encoding failures precede business fan-out.
	frozen, snapshot, err := rpc.FreezeParams(prepared.Params)
	if err != nil {
		return a.prepareFailureExit(state, started, req, err)
	}
	state.planPin = pin
	state.wireParams = frozen
	state.preparedJSON = snapshot
	// Freeze the invocation's routing facts once, from the final prepared
	// request and the invocation's captured mode: the pending-local route,
	// the business round's tip-freshness scope, and the processing route.
	// This is the last read of the prepared Go parameters — every later
	// phase reads these frozen facts, never the caller's or the prepared
	// request's mutable values.
	state.pendingLocal = planPendingLocalRoute(plan, prepared, state.snap, state.snapOK)
	state.freshness = preparedTipFreshness(plan, prepared)
	state.preparedMethod = prepared.Method
	// A plan processor resolves the canonical route for an ordinary strict
	// round, while pending-local shapes and forwarded modes keep the
	// complete raw route. Every later phase reads this frozen value; nothing
	// re-derives it.
	state.processorRoute = frozenProcessorRoute(plan, state.pendingLocal, state.snap, state.snapOK)
	// A frozen canonical route needs the resolver's canonical consumption
	// capability. The compiler rejects the mismatch for compiled specs; a
	// plan installed directly through WithPlans carries the same
	// requirement, detected here so the round takes the one internal
	// contract exit instead of reaching the resolution or any raw-hook
	// fallback.
	if state.processorRoute != nil {
		var ok bool
		state.canonicalResolver, ok = plan.Resolver.(rpc.CanonicalOutcomeResolver)
		if !ok {
			state.internalFault = fmt.Errorf("%w: %s resolver cannot consume canonical observations", rpc.ErrInvalidCanonicalResult, plan.Method)
		}
	}
	// The per-source stage reads the wire's semantic JSON once, shared
	// read-only across sources. Keep numeric tokens exact rather than
	// rounding them to float64. The original Go values remain available on
	// prepared for plan queries. A plan with a processor needs the same view
	// even without an admission hook — the processor receives the identical
	// semantic request the wire and Decision.Prepared carry.
	if state.plan.Admit != nil || state.processorRoute != nil {
		state.admissionRequest = rpc.Request{Method: prepared.Method}
		if snapshot != nil {
			decoder := json.NewDecoder(bytes.NewReader(snapshot))
			decoder.UseNumber()
			if err := decoder.Decode(&state.admissionRequest.Params); err != nil {
				return a.prepareFailureExit(state, started, req, fmt.Errorf("decode prepared parameters: %v: %w", err, rpc.ErrInvalidRequest))
			}
		}
	}
	return nil
}

// frozenProcessorRoute decides the invocation's processing route from the
// plan's declarations, the frozen pending-local fact, and the captured
// corroboration mode. The decision is by execution class — pending-local and
// forwarded rounds keep the complete raw route — never by method name or a
// second registry.
func frozenProcessorRoute(plan *rpc.ReadPlan, pendingLocal bool, snap rpc.Corroboration, snapOK bool) rpc.CanonicalProcessor {
	if plan.ProcessCanonical == nil {
		return nil
	}
	if pendingLocal {
		return nil
	}
	if snapOK && snap.Mode == rpc.CorroborationForwarded {
		return nil
	}
	return plan.ProcessCanonical
}

// preparedTipFreshness reports whether the prepared business request still
// depends on the sampled latest tip. A moving tag rewritten by Prepare has
// become an explicit pin, so the business round is exempt: an explicit
// height/hash is valid regardless of the source's current tip. A request that
// never carried a moving tag is exempt for the same reason. Reference
// establishment is scoped separately by the runtime, where the requested tag
// is still known. This is a prepare-time query over the final prepared
// request; its result freezes into the invocation and is never recomputed
// from the parameters afterwards.
func preparedTipFreshness(plan *rpc.ReadPlan, prepared rpc.Request) pool.TipFreshnessScope {
	request, needed := plan.ReferenceRequirement(prepared)
	if !needed || request.Kind != rpc.ReferenceLatest {
		return pool.TipFreshnessExempt
	}
	return pool.TipFreshnessLatest
}

// establishPlannedReference runs the declared establishment once per
// invocation. A read that requires a reference without an installed stage
// fails before any fan-out. The evidence the stage transfers — success or a
// failure's partial evidence — becomes this invocation's reference fact: the
// coordinator attaches success evidence to the invocation exactly once, keeps
// failure evidence visible to the decision, and never starts the business
// round without its reference. A stage that reports success without evidence
// fails the read with the reference-required sentinel instead of fabricating
// an empty reference.
func (a *Aggregator) establishPlannedReference(state *invocationState, started time.Time, req rpc.Request, request rpc.ReferenceRequest, ref *rpc.ReferenceEvidence, refOK *bool) *earlyExit {
	if a.stage == nil {
		err := fmt.Errorf("reference: no establishment stage for a read that requires one: %w", rpc.ErrReferenceRequired)
		return a.requestFailureExit(state, started, req, err)
	}
	evidence, err := a.stage.Establish(state.ctx, state.invocation, request)
	state.reference = evidence
	if err != nil {
		// A failed establishment keeps its evidence visible to the decision:
		// the business fan-out never starts. The error carries the same
		// "anchor:" prefix the preparing decorators wrap their cause with.
		err = fmt.Errorf("anchor: %w", err)
		return a.requestFailureExit(state, started, req, err)
	}
	if evidence == nil {
		// The stage reported success without evidence: fail the read with the
		// documented sentinel rather than ride a fabricated empty reference.
		err := fmt.Errorf("anchor: %w", rpc.ErrReferenceRequired)
		return a.requestFailureExit(state, started, req, err)
	}
	completed, attachErr := state.invocation.WithReference(*evidence)
	if attachErr != nil {
		// The invocation refused a second reference: the stage's evidence
		// stays the decision's, and the read fails internally — the business
		// round never starts.
		return a.requestFailureExit(state, started, req, fmt.Errorf("anchor: %w", attachErr))
	}
	state.invocation = completed
	*ref, *refOK = *evidence, true
	return nil
}

// prepareFailureExit records one planned prepare-phase failure: the raw-data
// trail gets the "prepare" record, and the decision carries the reference
// evidence captured so far.
func (a *Aggregator) prepareFailureExit(state *invocationState, started time.Time, req rpc.Request, err error) *earlyExit {
	if state.collector != nil {
		state.collector.Record("prepare", req.Method, rpc.SourceRef{}, nil, err.Error())
	}
	return a.requestFailureExit(state, started, req, err)
}

// requestFailureExit is the shared request-side failure exit of the prepare
// and establishment phases: no source was attempted, so the fault tables
// stay empty by contract, the returned error carries the cause, and the
// decision keeps the reference evidence captured so far. The fault tables go
// through the one invocation assembly point, so the planned path and the
// legacy path keep the same contract.
func (a *Aggregator) requestFailureExit(state *invocationState, started time.Time, req rpc.Request, err error) *earlyExit {
	decision := rpc.Decision{Status: rpc.Unknown, Reference: state.establishedReference(), Adjudication: rpc.Adjudication{Basis: state.invocation.Basis().Size(), Required: state.invocation.Basis().Required()}}
	state.attachFaults(&decision)
	a.recordDecision(started, req.Method, decision, err, nil, nil)
	return &earlyExit{decision: decision, err: err}
}

package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
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
	state.prepared = prepared
	state.planPin = pin
	state.wireParams = frozen
	state.preparedJSON = snapshot
	// Freeze the invocation's processing route once, from the prepared
	// request and the invocation's captured mode: a plan processor resolves
	// the canonical route for an ordinary strict round, while pending-local
	// shapes and forwarded modes keep the complete raw route. Every later
	// phase reads this frozen value; nothing re-derives it.
	state.processorRoute = frozenProcessorRoute(plan, prepared, state.snap, state.snapOK)
	// A frozen canonical route needs the resolver's canonical consumption
	// capability. The compiler rejects the mismatch for compiled specs; a
	// plan installed directly through WithPlans carries the same
	// requirement, detected here so the round takes the one internal
	// contract exit instead of reaching the resolution or any raw-hook
	// fallback.
	if state.processorRoute != nil {
		if _, ok := plan.Resolver.(rpc.CanonicalOutcomeResolver); !ok {
			state.internalFault = fmt.Errorf("%w: %s resolver cannot consume canonical observations", rpc.ErrInvalidCanonicalResult, plan.Method)
		}
	}
	// The per-source stage reads the wire's semantic JSON once, shared
	// read-only across sources. Keep numeric tokens exact rather than
	// rounding them to float64. The original Go values remain available on
	// prepared for plan queries. A plan with a processor needs the same view
	// even without an admission hook — the processor receives the identical
	// semantic request the wire and Decision.Prepared carry.
	if _, ok := state.admissionHook(); ok || state.processorRoute != nil {
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
// plan's declarations, the prepared request, and the captured corroboration
// mode. The decision is by execution class — pending-local and forwarded
// rounds keep the complete raw route — never by method name or a second
// registry.
func frozenProcessorRoute(plan *rpc.ReadPlan, prepared rpc.Request, snap rpc.Corroboration, snapOK bool) rpc.CanonicalProcessor {
	if plan.ProcessCanonical == nil {
		return nil
	}
	if planPendingLocalRoute(plan, prepared, snap, snapOK) {
		return nil
	}
	if snapOK && snap.Mode == rpc.CorroborationForwarded {
		return nil
	}
	return plan.ProcessCanonical
}

// establishPlannedReference runs the declared establishment once per
// invocation. A read that requires a reference without an installed stage
// fails before any fan-out; a failed establishment keeps its evidence on the
// capture and never starts the business round.
func (a *Aggregator) establishPlannedReference(state *invocationState, started time.Time, req rpc.Request, request rpc.ReferenceRequest, ref *rpc.ReferenceEvidence, refOK *bool) *earlyExit {
	if a.stage == nil {
		err := fmt.Errorf("reference: no establishment stage for a read that requires one: %w", rpc.ErrReferenceRequired)
		return a.requestFailureExit(state, started, req, err)
	}
	completed, err := a.stage.Establish(state.ctx, state.invocation, request)
	if err != nil {
		// A failed establishment keeps its evidence visible to the decision:
		// the business fan-out never starts. The error carries the same
		// "anchor:" prefix the preparing decorators wrap their cause with.
		err = fmt.Errorf("anchor: %w", err)
		return a.requestFailureExit(state, started, req, err)
	}
	if established, ok := completed.Reference(); ok {
		*ref, *refOK = established, true
		// The transitional capture stays the decision's evidence source.
		refcontext.Publish(state.ctx, established)
	}
	state.invocation = completed
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
	decision := rpc.Decision{Status: rpc.Unknown, Reference: state.capture.Reference()}
	state.attachFaults(&decision, nil)
	a.recordDecision(started, req.Method, state.validityPolicy(), decision, err, nil, nil)
	return &earlyExit{decision: decision, err: err}
}

// plannedAdmissionHook is the plan's same-source admission hook, or absent
// when the plan declares none.
func plannedAdmissionHook(plan *rpc.ReadPlan) (func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error), bool) {
	if plan.Admit == nil {
		return nil, false
	}
	return plan.Admit, true
}

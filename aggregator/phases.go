package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/uncertain"
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
	// Validate / Prepare: the method's own validation and its executable
	// pending rejection run before anything else and fail the read locally.
	if plan.Validate != nil {
		if err := plan.Validate(state.ctx, req); err != nil {
			return a.prepareFailureExit(state, started, req, err)
		}
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
	// Freeze the prepared parameters once: custom marshalers run here and
	// nowhere else, so the wire and the recorded prepared-request fact carry
	// the same bytes. An encode failure is an argument error (ErrInvalidRequest
	// from FreezeParams) before any fan-out. state.prepared keeps the prepared
	// Go values — plan queries and the admission hook read them — while the
	// fan-out sends the frozen list and the decision records the snapshot.
	frozen, snapshot, err := rpc.FreezeParams(prepared.Params)
	if err != nil {
		return a.prepareFailureExit(state, started, req, err)
	}
	state.prepared = prepared
	state.planPin = pin
	state.wireParams = frozen
	state.preparedJSON = snapshot
	return nil
}

// establishPlannedReference runs the declared establishment once per
// invocation. A read that requires a reference without an installed stage
// fails before any fan-out; a failed establishment keeps its evidence on the
// capture and never starts the business round.
func (a *Aggregator) establishPlannedReference(state *invocationState, started time.Time, req rpc.Request, request rpc.ReferenceRequest, ref *rpc.ReferenceEvidence, refOK *bool) *earlyExit {
	if a.stage == nil {
		err := fmt.Errorf("reference: no establishment stage for a read that requires one: %w", rpc.ErrReferenceRequired)
		return a.establishmentFailureExit(state, started, req, err)
	}
	completed, err := a.stage.Establish(state.ctx, state.invocation, request)
	if err != nil {
		// A failed establishment keeps its evidence visible to the decision:
		// the business fan-out never starts. The error carries the same
		// "anchor:" prefix the preparing decorators wrap their cause with.
		err = fmt.Errorf("anchor: %w", err)
		return a.establishmentFailureExit(state, started, req, err)
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
// evidence captured so far. The fault tables go through the one invocation
// assembly point, so the planned path and the legacy path keep the same
// contract: a request-side failure rides the returned error and the tables
// stay empty.
func (a *Aggregator) prepareFailureExit(state *invocationState, started time.Time, req rpc.Request, err error) *earlyExit {
	if c := uncertain.CollectorFromContext(state.ctx); c != nil {
		c.Record("prepare", req.Method, rpc.SourceRef{}, nil, err.Error())
	}
	decision := rpc.Decision{Status: rpc.Unknown, Reference: state.capture.Reference()}
	state.attachFaults(&decision, nil)
	a.recordDecision(started, req.Method, state.validityPolicy(), decision, err, nil, nil)
	return &earlyExit{decision: decision, err: err}
}

// establishmentFailureExit records one establishment failure. The raw
// establishment trail is already on the capture, so no second "prepare"
// record is written. The fault tables go through the one invocation
// assembly point, matching the legacy establishment exit.
func (a *Aggregator) establishmentFailureExit(state *invocationState, started time.Time, req rpc.Request, err error) *earlyExit {
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

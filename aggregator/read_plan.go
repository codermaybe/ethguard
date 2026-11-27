package aggregator

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// Every read runs one execution plan through the fixed invocation pipeline.
// Registered methods supply their plans; unknown methods use forwardPlan.

// forwardPlan is the explicit forward semantics for a method outside the
// registry: one bounded single-source attempt, the first usable answer wins,
// and no agreement is claimed (decision 0002). It rides the same fixed phase
// pipeline with an empty declaration set — no validation, no reference, no
// admission hook, no projection.
func forwardPlan(method string) rpc.ReadPlan {
	return rpc.ReadPlan{Method: method, Resolver: rpc.ForwardPolicy{}}
}

// WithPlans installs the read plans the aggregator executes through the
// fixed phase pipeline. A method without a plan uses forwardPlan. A non-empty plan with a local
// hook but no resolver is installed so its local checks still run, then fails
// closed during preparation. Empty plans without a resolver or hook are skipped.
func WithPlans(plans map[string]rpc.ReadPlan) Option {
	return func(a *Aggregator) {
		for method, plan := range plans {
			// A plan without a resolver and without a local hook is not
			// executable and is skipped; a validate-or-reject-only plan is
			// the local rejection shape and installs fine.
			if plan.Resolver != nil || plan.Prepare != nil || plan.PendingReject != nil {
				a.plans[method] = plan
			}
		}
	}
}

// resolvePlannedInvocation selects the request's frozen local/corroborating
// semantics. Every route returns the same mandatory source judgments.
func resolvePlannedInvocation(plan *rpc.ReadPlan, snap rpc.Corroboration, snapOK bool, pendingLocal bool, obs []rpc.Observation) (rpc.Outcome, error) {
	if plan == nil || plan.Resolver == nil {
		outcome := rpc.UnassessedOutcome(obs)
		outcome.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
		return outcome, fmt.Errorf("missing resolver: %w", rpc.ErrInvalidOutcome)
	}
	if singleSourcePolicy(plan.Resolver) {
		return plan.Resolver.Resolve(obs)
	}
	if pendingLocal {
		return rpc.ResolveLocalObservation(plan.Resolver, obs)
	}
	if snapOK && plan.Corroborating() {
		resolver, ok := plan.Resolver.(rpc.CorroboratingPolicy)
		if !ok {
			outcome := rpc.UnassessedOutcome(obs)
			outcome.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
			return outcome, fmt.Errorf("declared corroborating resolver lacks resolution entry: %w", rpc.ErrInvalidOutcome)
		}
		return resolver.ResolveCorroborated(snap, obs)
	}
	return plan.Resolver.Resolve(obs)
}

// planPendingLocalRoute is the plan's pending-local routing check, computed
// once at prepare time from the final prepared request: a pending request
// shape on a pending-observation method resolves as a node-local
// observation, except in Forwarded mode, where the pending shape still
// forwards. Its result freezes into the invocation; nothing recomputes it
// from the parameters after preparation.
func planPendingLocalRoute(plan *rpc.ReadPlan, prepared rpc.Request, snap rpc.Corroboration, snapOK bool) bool {
	if plan.Pending.Mode != rpc.PendingObserve {
		return false
	}
	if !pendingLocalShape(plan.Pending.Pos, prepared) {
		return false
	}
	return !(snapOK && snap.Mode == rpc.CorroborationForwarded)
}

// pendingLocalShape reports whether the prepared request addresses pending
// state at the declared position. It routes through the rpc package's single
// pending-grammar authority so the observe mode recognizes exactly the
// spellings the refuse path and the request validators recognize: raw
// strings, surrounding space, JSON strings, and EIP-1898 objects.
func pendingLocalShape(pos int, prepared rpc.Request) bool {
	if pos < 0 || pos >= len(prepared.Params) {
		return false
	}
	return rpc.IsPendingReference(prepared.Params[pos])
}

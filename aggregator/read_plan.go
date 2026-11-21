package aggregator

import (
	"github.com/codermaybe/ethguard/rpc"
)

// The planned-read seam. A registry drives one read method either from its
// explicit execution plan or from its compiled policy chain. Both run the
// same fixed phase pipeline over one invocation; the plan path reads every
// routing decision from the plan value instead of type-asserting optional
// capabilities off a decorator chain. A method without a plan — known but
// not yet migrated, or unknown — keeps the legacy policy path untouched.

// forwardPlan is the explicit forward semantics for a method outside the
// registry: one bounded single-source attempt, the first usable answer wins,
// and no agreement is claimed (decision 0002). It rides the same fixed phase
// pipeline with an empty declaration set — no validation, no reference, no
// admission hook, no projection.
func forwardPlan(method string) rpc.ReadPlan {
	return rpc.ReadPlan{Method: method, Resolver: rpc.ForwardPolicy{}}
}

// WithPlans installs the read plans the aggregator executes through the
// fixed phase pipeline. A method with a plan runs the plan path; every
// other method keeps the compiled policy path. A non-empty plan with a local
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

// resolvePlannedInvocation resolves one observation set from the plan's
// declared semantics: the invocation's frozen pending-local fact first, then
// the resolver's corroborated or plain outcome capability. The fallback chain
// mirrors the legacy invocation resolution exactly — the same capabilities,
// consulted in the same order — with the plan's declarations replacing the
// type assertions over decorator layers. Corroboration is gated on the plan's
// declared class (plan.Corroborating), and the resolver value's own
// capabilities are read straight off that value, never by walking decorators.
// pendingLocal is the invocation's prepare-time routing fact: resolution
// never re-reads the prepared parameters to re-derive it.
//
// The outcome is the resolution's one production fact. A resolver that states
// per-source verdicts passes its outcome through unchanged; a resolver with
// only the decision capability is adapted with nil verdicts — the statement
// stays empty rather than being fabricated, and classification falls back to
// the policy's own validity contract downstream.
func resolvePlannedInvocation(plan *rpc.ReadPlan, pendingLocal bool, snap rpc.Corroboration, snapOK bool, obs []rpc.Observation) (rpc.Outcome, error) {
	if pendingLocal {
		decision, err := rpc.ResolveLocalObservation(plan.Resolver, obs)
		return rpc.Outcome{Decision: decision}, err
	}
	if snapOK && plan.Corroborating() {
		if corrob, ok := plan.Resolver.(rpc.CorroboratingOutcomeResolver); ok {
			return corrob.ResolveOutcomeCorroborated(snap, obs)
		}
		if corrob, ok := plan.Resolver.(rpc.CorroboratingPolicy); ok {
			decision, err := corrob.ResolveCorroborated(snap, obs)
			return rpc.Outcome{Decision: decision}, err
		}
	}
	if outcomeResolver, ok := plan.Resolver.(rpc.OutcomeResolver); ok {
		return outcomeResolver.ResolveOutcome(obs)
	}
	decision, err := plan.Resolver.Resolve(obs)
	return rpc.Outcome{Decision: decision}, err
}

// resolveCanonicalInvocation resolves one processed observation set through
// the plan's canonical consumption capability. The entries mirror the raw
// chain exactly: the frozen snapshot adjudicates when the invocation captured
// one, and the resolver's construction-time configuration answers otherwise —
// there is no zero-snapshot shortcut for the corroborated entry, and the
// pending-local shape never reaches this function because the frozen route
// stays raw for it. The canonical capability is outcome-shaped by contract,
// so the outcome passes through unchanged.
func resolveCanonicalInvocation(plan *rpc.ReadPlan, snap rpc.Corroboration, snapOK bool, cobs []rpc.CanonicalObservation) (rpc.Outcome, error) {
	// The prepare phase already verified the canonical consumption capability
	// when it froze the route: a plan whose resolver cannot consume canonical
	// observations takes the one internal contract exit there and never
	// reaches resolution. The compiler rejects the same mismatch for compiled
	// specs.
	resolver := plan.Resolver.(rpc.CanonicalOutcomeResolver)
	if snapOK && plan.Corroborating() {
		return resolver.ResolveCanonicalCorroborated(snap, cobs)
	}
	return resolver.ResolveCanonical(cobs)
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

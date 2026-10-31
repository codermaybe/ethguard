package aggregator

import (
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// The planned-read seam. A registry drives one read method either from its
// explicit execution plan or from its compiled policy chain. Both run the
// same fixed phase pipeline over one invocation; the plan path reads every
// routing decision from the plan value instead of type-asserting optional
// capabilities off a decorator chain. A method without a plan — known but
// not yet migrated, or unknown — keeps the legacy policy path untouched.

// WithPlans installs the read plans the aggregator executes through the
// fixed phase pipeline. A method with a plan runs the plan path; every
// other method keeps the compiled policy path. Plans without a resolver
// value are not executable and are skipped.
func WithPlans(plans map[string]rpc.ReadPlan) Option {
	return func(a *Aggregator) {
		for method, plan := range plans {
			if plan.Resolver != nil {
				a.plans[method] = plan
			}
		}
	}
}

// resolvePlannedInvocation resolves one observation set from the plan's
// declared semantics: the pending-local route first, then the resolver's
// corroborated or plain outcome capability. The fallback chain mirrors the
// legacy invocation resolution exactly — the same capabilities, consulted in
// the same order — with the plan's declarations replacing the type
// assertions over decorator layers. Corroboration is gated on the plan's
// declared class (plan.Corroborating), and the resolver value's own
// capabilities are read straight off that value, never by walking decorators.
func resolvePlannedInvocation(plan *rpc.ReadPlan, prepared rpc.Request, snap rpc.Corroboration, snapOK bool, obs []rpc.Observation) invocationResolution {
	if planPendingLocalRoute(plan, prepared, snap, snapOK) {
		decision, err := rpc.ResolveLocalObservation(plan.Resolver, obs)
		return invocationResolution{decision: decision, err: err}
	}
	if snapOK && plan.Corroborating() {
		if corrob, ok := plan.Resolver.(rpc.CorroboratingOutcomeResolver); ok {
			outcome, err := corrob.ResolveOutcomeCorroborated(snap, obs)
			return invocationResolution{decision: outcome.Decision, verdicts: outcome.Verdicts, err: err}
		}
		if corrob, ok := plan.Resolver.(rpc.CorroboratingPolicy); ok {
			decision, err := corrob.ResolveCorroborated(snap, obs)
			return invocationResolution{decision: decision, err: err}
		}
	}
	if outcomeResolver, ok := plan.Resolver.(rpc.OutcomeResolver); ok {
		outcome, err := outcomeResolver.ResolveOutcome(obs)
		return invocationResolution{decision: outcome.Decision, verdicts: outcome.Verdicts, err: err}
	}
	decision, err := plan.Resolver.Resolve(obs)
	return invocationResolution{decision: decision, err: err}
}

// planPendingLocalRoute is the plan's pending-local routing check: a pending
// request shape on a pending-observation method resolves as a node-local
// observation, except in Forwarded mode, where the pending shape still
// forwards.
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
// state at the declared position, in string or EIP-1898 object spelling.
func pendingLocalShape(pos int, prepared rpc.Request) bool {
	if pos < 0 || pos >= len(prepared.Params) {
		return false
	}
	switch ref := prepared.Params[pos].(type) {
	case string:
		return strings.EqualFold(ref, "pending")
	case map[string]any:
		number, ok := ref["blockNumber"].(string)
		return ok && strings.EqualFold(number, "pending")
	}
	return false
}

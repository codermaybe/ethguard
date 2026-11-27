package rpcengine

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// ResolveLocalObservation resolves one observation set as a node-local
// pending observation (decision 0035): the first source with a normalizable
// answer stands with Selected status and its source as Support — unanimous
// agreement stays Selected, because agreement over pending state is
// coincidence of independent node views, not corroboration of a
// deterministic truth. No usable source fails Unknown with
// ErrNoValidResponse.
//
// The policy argument is the method's resolver value: normalization and the
// canonical encoder are the resolver's own, and the value domain includes
// structured RPC error bodies only when the resolver opted in. The plan
// selects this entry for its pending-local route.
func ResolveLocalObservation(normalize func(json.RawMessage) (string, error), policy Resolver, obs []rpc.Observation) (Outcome, error) {
	return resolveLocal(normalize, policy, obs, true)
}

func resolveLocal(normalize func(json.RawMessage) (string, error), policy Resolver, obs []rpc.Observation, pending bool) (Outcome, error) {
	includeProtocol := false
	if provider, ok := policy.(RPCErrorResolver); ok {
		includeProtocol = provider.IncludeRPCError()
	}
	verdicts := initialVerdicts(obs)
	decision := rpc.Decision{Adjudication: rpc.Adjudication{Basis: len(obs), Required: 1, Reason: rpc.ReasonNoCandidate}}
	for i, item := range obs {
		v := &verdicts[i]
		value, candidateErr := item.Response.Candidate(includeProtocol)
		if candidateErr != nil {
			if item.Response.CandidateErr(true) == nil {
				v.Kind, v.Method = rpc.VerdictUnusable, rpc.Interpretation{State: rpc.InterpretationExcluded, Cause: candidateErr}
			} else {
				unavailableVerdict(v, item.Response)
			}
			continue
		}
		if _, err := normalize(value); err != nil {
			rejectVerdict(v, err)
			continue
		}
		if canonical, err := CanonicalOf(policy, value); err == nil {
			value = canonical
		} else if !errors.Is(err, rpc.ErrNotCanonicalScalar) {
			rejectVerdict(v, err)
			return completeOutcome(obs, decision, verdicts, fmt.Errorf("local encode: %w", err))
		}
		v.Kind, v.Method = rpc.VerdictSupported, rpc.Interpretation{State: rpc.InterpretationValidated}
		decision.Status, decision.Value, decision.Support = rpc.Selected, value, []rpc.SourceRef{item.Source}
		decision.Adjudication.Reason = rpc.ReasonAccepted
		decision.Adjudication.Candidates = []rpc.CandidateGroup{{Sources: decision.Support}}
		if pending {
			decision.Provenance = append(json.RawMessage(nil), item.Evidence...)
		}
		return completeOutcome(obs, decision, verdicts, nil)
	}
	return completeOutcome(obs, decision, verdicts, noValidResponse("local", len(obs)))
}

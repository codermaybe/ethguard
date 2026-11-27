package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
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
func ResolveLocalObservation(policy Policy, obs []Observation) (Outcome, error) {
	return resolveLocal(policy, obs, true)
}

func resolveLocal(policy Policy, obs []Observation, pending bool) (Outcome, error) {
	includeProtocol := false
	if provider, ok := policy.(RPCErrorPolicy); ok {
		includeProtocol = provider.IncludeRPCError()
	}
	verdicts := initialVerdicts(obs)
	decision := Decision{Adjudication: Adjudication{Basis: len(obs), Required: 1, Reason: ReasonNoCandidate}}
	for i, item := range obs {
		v := &verdicts[i]
		value, candidateErr := item.Response.Candidate(includeProtocol)
		if candidateErr != nil {
			if item.Response.CandidateErr(true) == nil {
				v.Kind, v.Method = VerdictUnusable, Interpretation{State: InterpretationExcluded, Cause: candidateErr}
			} else {
				unavailableVerdict(v, item.Response)
			}
			continue
		}
		if _, err := policy.Normalize(value); err != nil {
			rejectVerdict(v, err)
			continue
		}
		if canonical, err := CanonicalOf(policy, value); err == nil {
			value = canonical
		} else if !errors.Is(err, ErrNotCanonicalScalar) {
			rejectVerdict(v, err)
			return completeOutcome(obs, decision, verdicts, fmt.Errorf("local encode: %w", err))
		}
		v.Kind, v.Method = VerdictSupported, Interpretation{State: InterpretationValidated}
		decision.Status, decision.Value, decision.Support = Selected, value, []SourceRef{item.Source}
		decision.Adjudication.Reason = ReasonAccepted
		decision.Adjudication.Candidates = []CandidateGroup{{Sources: decision.Support}}
		if pending {
			decision.Provenance = append(json.RawMessage(nil), item.Evidence...)
		}
		return completeOutcome(obs, decision, verdicts, nil)
	}
	return completeOutcome(obs, decision, verdicts, noValidResponse("local", len(obs)))
}

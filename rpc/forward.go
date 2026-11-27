package rpc

import (
	"encoding/json"
	"fmt"
)

// ForwardPolicy returns the first successful observation without claiming any
// agreement between sources. It is the fallback for unknown methods.
type ForwardPolicy struct{}

// SingleSource marks the policy as requiring ordered one-source forwarding.
func (ForwardPolicy) SingleSource() bool { return true }

// IncludeRPCError preserves authoritative protocol answers in observations.
func (ForwardPolicy) IncludeRPCError() bool { return true }

func (ForwardPolicy) Normalize(value json.RawMessage) (string, error) { return string(value), nil }
func (ForwardPolicy) Resolve(obs []Observation) (Outcome, error) {
	verdicts := initialVerdicts(obs)
	decision := Decision{Adjudication: Adjudication{Basis: len(obs), Required: 1, Reason: ReasonNoCandidate}}
	for i, item := range obs {
		if item.Response.CandidateErr(true) != nil {
			unavailableVerdict(&verdicts[i], item.Response)
			continue
		}
		verdicts[i].Kind, verdicts[i].Method = VerdictSupported, Interpretation{State: InterpretationValidated}
		decision.Support = []SourceRef{item.Source}
		decision.Status = Forwarded
		decision.Adjudication.Reason = ReasonAccepted
		decision.Adjudication.Candidates = []CandidateGroup{{Sources: decision.Support}}
		if item.Response.Answer.Kind == AnswerProtocolError {
			return completeOutcome(obs, decision, verdicts, item.Response.Answer.Cause)
		}
		decision.Status, decision.Value = Forwarded, item.Response.Answer.Result
		return completeOutcome(obs, decision, verdicts, nil)
	}
	return completeOutcome(obs, decision, verdicts, fmt.Errorf("forward: no source responded: %w", ErrNoValidResponse))
}

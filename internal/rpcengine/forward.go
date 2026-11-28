package rpcengine

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// ForwardResolver returns the first successful observation without claiming any
// agreement between sources. It is the fallback for unknown methods.
type ForwardResolver struct{}

// SingleSource marks the policy as requiring ordered one-source forwarding.
func (ForwardResolver) SingleSource() bool { return true }

// IncludeRPCError preserves authoritative protocol answers in observations.
func (ForwardResolver) IncludeRPCError() bool { return true }

func (ForwardResolver) Normalize(value json.RawMessage) (string, error) { return string(value), nil }

func (ForwardResolver) Resolve(obs []rpc.Observation) (Outcome, error) {
	verdicts := initialVerdicts(obs)
	decision := rpc.Decision{Adjudication: rpc.Adjudication{Basis: len(obs), Required: 1, Reason: rpc.ReasonNoCandidate}}
	for i, item := range obs {
		if item.Response.CandidateErr(true) != nil {
			unavailableVerdict(&verdicts[i], item.Response)
			continue
		}
		verdicts[i].Kind, verdicts[i].Method = rpc.VerdictSupported, rpc.Interpretation{State: rpc.InterpretationValidated}
		decision.Support = []rpc.SourceRef{item.Source}
		decision.Status = rpc.Forwarded
		decision.Adjudication.Reason = rpc.ReasonAccepted
		decision.Adjudication.Candidates = []rpc.CandidateGroup{{Sources: decision.Support}}
		if item.Response.Answer.Kind == rpc.AnswerProtocolError {
			return completeOutcome(obs, decision, verdicts, item.Response.Answer.Cause)
		}
		decision.Status, decision.Value = rpc.Forwarded, item.Response.Answer.Result
		return completeOutcome(obs, decision, verdicts, nil)
	}
	return completeOutcome(obs, decision, verdicts, fmt.Errorf("forward: no source responded: %w", rpc.ErrNoValidResponse))
}

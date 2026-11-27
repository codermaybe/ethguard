package rpcengine

import (
	"errors"
	"fmt"
	"sort"

	"github.com/codermaybe/ethguard/rpc"
)

// WriteQuorum computes the support a hash cluster must reach for
// WriteAccepted over a voting basis of n members with caller floor h:
// Q = max(h, floor(n/2)+1) — the same shape as the read-side
// Corroboration.Required, applied to propagation rather than value truth.
func WriteQuorum(n, h int) int {
	q := n/2 + 1
	if h > q {
		q = h
	}
	return q
}

// ResolveWriteBroadcast clusters already interpreted write answers against
// the frozen basis and binds the winner to the locally submitted transaction.
// Minority dissent does not defeat a quorum; a foreign winning hash does.
func ResolveWriteBroadcast(basis int, rule Corroboration, expectedHash string, answers []rpc.SourceAnswer, faults []error) (rpc.WriteDecision, error) {
	decision := rpc.WriteDecision{SubmittedHash: expectedHash, Sources: answers,
		Adjudication: rpc.Adjudication{Basis: basis, Required: rule.Required(basis), Reason: rpc.ReasonNoCandidate},
	}.Clone()
	decision.Faults = append([]error(nil), faults...)
	if rule.Mode == rpc.CorroborationForwarded {
		decision.Adjudication.Required = 1
	}
	invalid := func(message string) (rpc.WriteDecision, error) {
		decision.Adjudication.Reason = rpc.ReasonInternalFailure
		return decision, fmt.Errorf("write: %s: %w", message, rpc.ErrInvalidOutcome)
	}
	if expectedHash == "" || basis < 0 || basis != len(answers) || rule.Mode < rpc.CorroborationMajority || rule.Mode > rpc.CorroborationForwarded || rule.Floor < 0 {
		return invalid("missing submitted hash or inconsistent voting basis")
	}
	clusters := make(map[string][]rpc.SourceRef)
	seen := make(map[string]bool, len(answers))
	rejected, malformed := 0, 0
	for i := range decision.Sources {
		answer := &decision.Sources[i]
		if answer.Attempted {
			decision.Status = rpc.WriteUnconfirmed
		}
		if answer.Source.ID == "" || seen[answer.Source.ID] {
			return invalid("duplicate or missing source identity")
		}
		if !answer.Attempted && (answer.Exchange.Progress != rpc.ExchangeNotStarted || answer.Answer.Kind != rpc.AnswerUnassessed) {
			return invalid("unattempted source carries an exchange or answer")
		}
		if err := answer.Answer.Validate(answer.Exchange); err != nil {
			return invalid("invalid source response summary")
		}
		switch answer.Answer.Kind {
		case rpc.AnswerUnassessed:
			if answer.Answer.Cause != nil || (answer.Attempted && answer.Exchange.Cause == nil) {
				return invalid("unassessed answer without termination")
			}
		case rpc.AnswerResult:
			if answer.Answer.Cause != nil {
				return invalid("result with protocol cause")
			}
		case rpc.AnswerMissing, rpc.AnswerInvalid, rpc.AnswerProtocolError:
			if answer.Answer.Cause == nil {
				return invalid("missing answer cause")
			}
		}
		seen[answer.Source.ID] = true
		answer.Relation = rpc.VerdictUnusable
		if !answer.Attempted {
			answer.Relation = rpc.VerdictAbsent
		}
		switch {
		case answer.Hash != "":
			if answer.Rejection != nil || answer.Answer.Kind != rpc.AnswerResult || answer.Answer.Interpretation.State != rpc.InterpretationValidated || answer.Exchange.Progress != rpc.ExchangeCompleted || answer.Exchange.Cause != nil {
				return invalid("hash without validated result")
			}
			clusters[answer.Hash] = append(clusters[answer.Hash], answer.Source)
		case answer.Rejection != nil:
			var protocol rpc.ProtocolError
			if answer.Answer.Kind != rpc.AnswerProtocolError || answer.Answer.Interpretation.State != rpc.InterpretationValidated || answer.Exchange.Cause != nil || !errors.As(answer.Answer.Cause, &protocol) || protocol.ErrorCode() != answer.Rejection.Code {
				return invalid("rejection without validated protocol error")
			}
			rejected++
		case answer.Exchange.Cause != nil:
			// A communication termination retains any answer already observed.
		case answer.Answer.Interpretation.State == rpc.InterpretationInternalFailure:
			return invalid("internal write interpretation failure")
		case answer.Answer.Kind == rpc.AnswerProtocolError:
			return invalid("protocol answer without write rejection")
		case answer.Answer.Kind == rpc.AnswerResult && answer.Answer.Interpretation.State != rpc.InterpretationRejected:
			return invalid("result without a write method judgment")
		case answer.Answer.Kind == rpc.AnswerInvalid || (answer.Answer.Kind == rpc.AnswerResult && answer.Answer.Interpretation.State == rpc.InterpretationRejected):
			malformed++
		}
	}
	hashes := make([]string, 0, len(clusters))
	for hash := range clusters {
		hashes = append(hashes, hash)
	}
	sort.Slice(hashes, func(i, j int) bool {
		if len(clusters[hashes[i]]) != len(clusters[hashes[j]]) {
			return len(clusters[hashes[i]]) > len(clusters[hashes[j]])
		}
		return hashes[i] < hashes[j]
	})
	for _, hash := range hashes {
		decision.Adjudication.Candidates = append(decision.Adjudication.Candidates, rpc.CandidateGroup{Sources: clusters[hash]})
	}
	agreement := 0
	if len(hashes) > 0 {
		decision.Hash = hashes[0]
		agreement = len(clusters[decision.Hash])
	}
	for i := range decision.Sources {
		answer := &decision.Sources[i]
		if answer.Hash != "" {
			answer.Relation = rpc.VerdictDissented
			if answer.Hash == decision.Hash {
				answer.Relation = rpc.VerdictSupported
			}
		}
	}
	if len(hashes) > 1 && agreement < decision.Adjudication.Required {
		decision.Adjudication.Reason = rpc.ReasonConflictingCandidates
		return decision, fmt.Errorf("write: %d hash groups below required support %d: %w", len(hashes), decision.Adjudication.Required, rpc.ErrConflict)
	}
	if decision.Hash != "" && decision.Hash != decision.SubmittedHash {
		decision.Adjudication.Reason = rpc.ReasonHashMismatch
		return decision, fmt.Errorf("write: reported hash %s differs from submitted hash %s: %w", decision.Hash, decision.SubmittedHash, rpc.ErrHashMismatch)
	}
	if agreement >= decision.Adjudication.Required {
		decision.Status, decision.Adjudication.Reason = rpc.WriteAccepted, rpc.ReasonAccepted
		return decision, nil
	}
	if len(hashes) == 1 {
		decision.Adjudication.Reason = rpc.ReasonInsufficientSupport
		return decision, fmt.Errorf("write: hash agreement %d below required %d: %w", agreement, decision.Adjudication.Required, rpc.ErrInsufficientSupport)
	}
	if malformed > 0 {
		return decision, fmt.Errorf("write: no method-valid hash: %w", rpc.ErrMalformedResponse)
	}
	if rejected > 0 {
		decision.Adjudication.Reason = rpc.ReasonRejected
		return decision, fmt.Errorf("write: answering sources rejected: %w", rpc.ErrNoValidResponse)
	}
	decision.Adjudication.Reason = rpc.ReasonUnassessed
	return decision, fmt.Errorf("write: no protocol answer evaluated: %w", rpc.ErrNoValidResponse)
}

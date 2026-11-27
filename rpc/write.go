package rpc

import (
	"errors"
	"fmt"
	"sort"
)

// WriteMethodEthSendRawTransaction is the JSON-RPC method the write pipeline
// broadcasts. It is deliberately the only write method in scope (decision-map
// #4): the same raw bytes re-sent to several sources is idempotent — every
// honest node derives the same transaction hash from them — so re-broadcast
// and catch-up re-sends stay safe. Account-managed eth_sendTransaction has
// no such idempotence and stays out of scope.
const WriteMethodEthSendRawTransaction = "eth_sendRawTransaction"

// WriteStatus classifies one broadcast outcome. The write path cannot reuse
// read Status: a minority of dissenting hash answers must not fail a
// propagation that reached quorum, and a receipt is a separate, later phase.
type WriteStatus int

const (
	// WriteUnknown is the zero value used when no write decision was made, for
	// example because request validation failed before contacting a source.
	WriteUnknown WriteStatus = iota
	// WriteAccepted means an agreeing hash cluster met the write quorum over
	// the full voting basis.
	WriteAccepted
	// WriteUnconfirmed means at least one attempt ran, but the propagation
	// condition was not confirmed. Adjudication and Sources carry the reasons.
	WriteUnconfirmed
)

// String names the write status for observability.
func (s WriteStatus) String() string {
	switch s {
	case WriteAccepted:
		return "accepted"
	case WriteUnconfirmed:
		return "unconfirmed"
	default:
		return "unknown"
	}
}

// SourceAnswer is one source's outcome in a broadcast, classified at the
// write boundary. Answers keep the source identity so callers can follow up
// per source (for example, re-broadcasting to a source whose answer arrived
// late).
type SourceAnswer struct {
	SourceSummary
	// Hash is the accepted transaction hash this source reported. Empty when
	// Rejected or unreachable. The wire value is normalized to lower-case by
	// the client before it reaches the resolver.
	Hash string
	// Rejection is a structured protocol error body from the source —
	// "nonce too low", replacement underpricing, an execution revert, and
	// similar. It is evidence on the decision: nothing here classifies a
	// rejection body as acceptance or as a permanent failure, because that
	// reading is a txpool-behaviour question this layer does not decide.
	Rejection *RPCError
}

// WriteDecision is the relative-trust result of one broadcast. It never
// claims Ethereum consensus finality: Accepted means a hash agreement reached the
// configured support bound across the voting basis, with every other
// outcome preserved as evidence.
type WriteDecision struct {
	Status WriteStatus
	// SubmittedHash is frozen from the local signed transaction before broadcast.
	SubmittedHash string
	// Hash is the leading reported hash, even when support or binding fails.
	Hash         string
	Sources      []SourceAnswer
	Adjudication Adjudication
	// Faults contains collection-level failures. Source causes live in Sources.
	Faults []error
}

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
func ResolveWriteBroadcast(basis int, rule Corroboration, expectedHash string, answers []SourceAnswer, faults []error) (WriteDecision, error) {
	decision := WriteDecision{SubmittedHash: expectedHash, Sources: cloneSourceAnswers(answers),
		Adjudication: Adjudication{Basis: basis, Required: rule.Required(basis), Reason: ReasonNoCandidate},
		Faults:       append([]error(nil), faults...)}
	if rule.Mode == CorroborationForwarded {
		decision.Adjudication.Required = 1
	}
	invalid := func(message string) (WriteDecision, error) {
		decision.Adjudication.Reason = ReasonInternalFailure
		return decision, fmt.Errorf("write: %s: %w", message, ErrInvalidOutcome)
	}
	if expectedHash == "" || basis < 0 || basis != len(answers) || rule.Mode < CorroborationMajority || rule.Mode > CorroborationForwarded || rule.Floor < 0 {
		return invalid("missing submitted hash or inconsistent voting basis")
	}
	clusters := make(map[string][]SourceRef)
	seen := make(map[string]bool, len(answers))
	rejected, malformed := 0, 0
	for i := range decision.Sources {
		answer := &decision.Sources[i]
		if answer.Attempted {
			decision.Status = WriteUnconfirmed
		}
		if answer.Source.ID == "" || seen[answer.Source.ID] {
			return invalid("duplicate or missing source identity")
		}
		if !answer.Attempted && (answer.Exchange.Progress != ExchangeNotStarted || answer.Answer.Kind != AnswerUnassessed) {
			return invalid("unattempted source carries an exchange or answer")
		}
		if err := validateResponseFacts(answer.Exchange, answer.Answer.Kind, answer.Answer.Interpretation); err != nil {
			return invalid("invalid source response summary")
		}
		switch answer.Answer.Kind {
		case AnswerUnassessed:
			if answer.Answer.Cause != nil || (answer.Attempted && answer.Exchange.Cause == nil) {
				return invalid("unassessed answer without termination")
			}
		case AnswerResult:
			if answer.Answer.Cause != nil {
				return invalid("result with protocol cause")
			}
		case AnswerMissing, AnswerInvalid, AnswerProtocolError:
			if answer.Answer.Cause == nil {
				return invalid("missing answer cause")
			}
		}
		seen[answer.Source.ID] = true
		answer.Relation = VerdictUnusable
		if !answer.Attempted {
			answer.Relation = VerdictAbsent
		}
		switch {
		case answer.Hash != "":
			if answer.Rejection != nil || answer.Answer.Kind != AnswerResult || answer.Answer.Interpretation.State != InterpretationValidated || answer.Exchange.Progress != ExchangeCompleted || answer.Exchange.Cause != nil {
				return invalid("hash without validated result")
			}
			clusters[answer.Hash] = append(clusters[answer.Hash], answer.Source)
		case answer.Rejection != nil:
			var protocol ProtocolError
			if answer.Answer.Kind != AnswerProtocolError || answer.Answer.Interpretation.State != InterpretationValidated || answer.Exchange.Cause != nil || !errors.As(answer.Answer.Cause, &protocol) || protocol.ErrorCode() != answer.Rejection.Code {
				return invalid("rejection without validated protocol error")
			}
			rejected++
		case answer.Exchange.Cause != nil:
			// A communication termination retains any answer already observed.
		case answer.Answer.Interpretation.State == InterpretationInternalFailure:
			return invalid("internal write interpretation failure")
		case answer.Answer.Kind == AnswerProtocolError:
			return invalid("protocol answer without write rejection")
		case answer.Answer.Kind == AnswerResult && answer.Answer.Interpretation.State != InterpretationRejected:
			return invalid("result without a write method judgment")
		case answer.Answer.Kind == AnswerInvalid || (answer.Answer.Kind == AnswerResult && answer.Answer.Interpretation.State == InterpretationRejected):
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
		decision.Adjudication.Candidates = append(decision.Adjudication.Candidates, CandidateGroup{Sources: clusters[hash]})
	}
	agreement := 0
	if len(hashes) > 0 {
		decision.Hash = hashes[0]
		agreement = len(clusters[decision.Hash])
	}
	for i := range decision.Sources {
		answer := &decision.Sources[i]
		if answer.Hash != "" {
			answer.Relation = VerdictDissented
			if answer.Hash == decision.Hash {
				answer.Relation = VerdictSupported
			}
		}
	}
	if len(hashes) > 1 && agreement < decision.Adjudication.Required {
		decision.Adjudication.Reason = ReasonConflictingCandidates
		return decision, fmt.Errorf("write: %d hash groups below required support %d: %w", len(hashes), decision.Adjudication.Required, ErrConflict)
	}
	if decision.Hash != "" && decision.Hash != decision.SubmittedHash {
		decision.Adjudication.Reason = ReasonHashMismatch
		return decision, fmt.Errorf("write: reported hash %s differs from submitted hash %s: %w", decision.Hash, decision.SubmittedHash, ErrHashMismatch)
	}
	if agreement >= decision.Adjudication.Required {
		decision.Status, decision.Adjudication.Reason = WriteAccepted, ReasonAccepted
		return decision, nil
	}
	if len(hashes) == 1 {
		decision.Adjudication.Reason = ReasonInsufficientSupport
		return decision, fmt.Errorf("write: hash agreement %d below required %d: %w", agreement, decision.Adjudication.Required, ErrInsufficientSupport)
	}
	if malformed > 0 {
		return decision, fmt.Errorf("write: no method-valid hash: %w", ErrMalformedResponse)
	}
	if rejected > 0 {
		decision.Adjudication.Reason = ReasonRejected
		return decision, fmt.Errorf("write: answering sources rejected: %w", ErrNoValidResponse)
	}
	decision.Adjudication.Reason = ReasonUnassessed
	return decision, fmt.Errorf("write: no protocol answer evaluated: %w", ErrNoValidResponse)
}

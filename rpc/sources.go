package rpc

import "errors"

// VerdictKind records the relation to this resolution, independently of the
// protocol answer and method interpretation. Zero makes no support claim.
type VerdictKind uint8

const (
	VerdictUnassessed VerdictKind = iota
	VerdictSupported
	VerdictDissented
	VerdictUnusable
	VerdictAbsent
	VerdictExcluded
)

func (k VerdictKind) String() string {
	switch k {
	case VerdictUnassessed:
		return "unassessed"
	case VerdictSupported:
		return "supported"
	case VerdictDissented:
		return "dissented"
	case VerdictUnusable:
		return "unusable"
	case VerdictAbsent:
		return "absent"
	case VerdictExcluded:
		return "excluded"
	default:
		return "invalid"
	}
}

// AnswerSummary carries protocol and interpretation facts without a result
// payload. Errors are immutable, as in Response.
type AnswerSummary struct {
	Kind           AnswerKind
	Cause          error
	Interpretation Interpretation
}

// SourceSummary is the default public evidence for one source. Protocol error
// payloads needed for a write rejection are retained by the write decision.
type SourceSummary struct {
	Attempted bool
	Source    SourceRef
	Exchange  Exchange
	Answer    AnswerSummary
	Relation  VerdictKind
}

var ErrInvalidOutcome = errors.New("rpc: invalid resolver outcome")

// Validate checks communication and interpretation states without requiring
// a result payload. Response and its summary share the same fact invariants.
func (a AnswerSummary) Validate(exchange Exchange) error {
	return validateResponseFacts(exchange, a.Kind, a.Interpretation)
}

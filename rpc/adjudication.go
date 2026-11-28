package rpc

import "fmt"

// DecisionReason is the conclusion formed at adjudication, independent of
// transport errors and of whether minority disagreement was observed.
type DecisionReason uint8

const (
	ReasonUnassessed DecisionReason = iota
	ReasonAccepted
	ReasonNoCandidate
	ReasonInsufficientSupport
	ReasonConflictingCandidates
	ReasonGuaranteeNotMet
	ReasonHashMismatch
	ReasonInternalFailure
	ReasonRejected
)

// CandidateGroup records membership without retaining another raw payload.
type CandidateGroup struct{ Sources []SourceRef }

// Adjudication records the fixed denominator and the groups actually observed.
// Multiple groups can coexist with ReasonAccepted under a majority policy.
type Adjudication struct {
	Basis      int
	Required   int
	Candidates []CandidateGroup
	Reason     DecisionReason
}

func (a Adjudication) Clone() Adjudication {
	if a.Candidates != nil {
		groups := make([]CandidateGroup, len(a.Candidates))
		for i, group := range a.Candidates {
			groups[i].Sources = cloneSourceRefs(group.Sources)
		}
		a.Candidates = groups
	}
	return a
}

// CorroborationMode selects how clustered admissible answers are adjudicated
// against the invocation's fixed voting basis (decision 0028).
type CorroborationMode int

const (
	// CorroborationMajority is the default mode: the winning cluster must
	// hold a strict majority of the voting basis and the support floor; a
	// minority of dissenting admissible answers does not fail the call.
	CorroborationMajority CorroborationMode = iota
	// CorroborationUnanimity additionally requires every admissible answer
	// to agree. It applies the same support floor and majority bound, so a
	// smaller surviving set cannot succeed solely because survivors agree.
	CorroborationUnanimity
	// CorroborationForwarded is intentional single-source mode. The selected
	// source is still subject to the method policy's local normalization and
	// binding checks, but no corroboration claim is made.
	CorroborationForwarded
)

// String names the mode for diagnostics.
func (m CorroborationMode) String() string {
	switch m {
	case CorroborationMajority:
		return "majority"
	case CorroborationUnanimity:
		return "unanimity"
	case CorroborationForwarded:
		return "forwarded"
	default:
		return fmt.Sprintf("corroboration(%d)", int(m))
	}
}

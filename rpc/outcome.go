package rpc

// VerdictKind is one resolver's classification of one source's answer.
type VerdictKind uint8

const (
	// VerdictSupported means the source's admissible answer won: it is part
	// of the decision's Support.
	VerdictSupported VerdictKind = iota
	// VerdictDissented means the source delivered an admissible answer that
	// lost the adjudication: a dissent, not a failure.
	VerdictDissented
	// VerdictUnusable means the source was attempted but provided no answer
	// the resolver could use: a transport fault, a timeout, a malformed
	// payload, or a protocol error outside the policy's value domain.
	VerdictUnusable
	// VerdictAbsent means the source was never attempted: a scheduling gate
	// paused its request while it kept its voting-basis slot. The resolver
	// observes only attempted work, so this kind is finalized by the
	// coordinator, which knows attempt bookkeeping; a resolver never emits it.
	VerdictAbsent
)

// String names the verdict for diagnostics.
func (k VerdictKind) String() string {
	switch k {
	case VerdictSupported:
		return "supported"
	case VerdictDissented:
		return "dissented"
	case VerdictUnusable:
		return "unusable"
	case VerdictAbsent:
		return "absent"
	default:
		return "unknown"
	}
}

// SourceVerdict is one source's classification in one resolution. Err carries
// the source's own failure for unusable verdicts; supported and dissented
// verdicts carry no error. The identity is always present: a verdict without
// a source cannot be projected into per-source evidence.
type SourceVerdict struct {
	Source SourceRef
	Kind   VerdictKind
	Err    error
}

// Outcome is the complete per-source result of one resolution: the decision
// and every source's verdict. Resolvers that can speak this shape say once,
// here, how each source fared; downstream consumers (fault lists, observability
// events) project from the outcome instead of re-running value classification
// with a second Normalize pass.
type Outcome struct {
	// Decision is the same decision the resolver's Resolve would return.
	Decision Decision
	// Verdicts covers every observation the resolver received, ordered by
	// Source.ID (name breaking ties). The order is diagnostic determinism,
	// not evidence.
	Verdicts []SourceVerdict
}

// VerdictFor returns the verdict recorded for one source ID.
func (o Outcome) VerdictFor(id string) (SourceVerdict, bool) {
	for _, verdict := range o.Verdicts {
		if verdict.Source.ID == id {
			return verdict, true
		}
	}
	return SourceVerdict{}, false
}

func sortSourceVerdicts(verdicts []SourceVerdict) {
	for i := 1; i < len(verdicts); i++ {
		for j := i; j > 0 && sourceRefLess(verdicts[j].Source, verdicts[j-1].Source); j-- {
			verdicts[j], verdicts[j-1] = verdicts[j-1], verdicts[j]
		}
	}
}

// OutcomeResolver is the optional capability of a resolver that reports every
// source's verdict alongside its decision. A policy may implement it;
// the coordinator prefers it and falls back to plain Resolve otherwise. The
// public Policy signature does not change.
type OutcomeResolver interface {
	ResolveOutcome(obs []Observation) (Outcome, error)
}

// CorroboratingOutcomeResolver mirrors ResolveCorroborated for policies whose
// resolution is adjudicated against an invocation's frozen corroboration
// snapshot. Implementing it also selects the fixed-basis collection mode, the
// same way CorroboratingPolicy does.
type CorroboratingOutcomeResolver interface {
	ResolveOutcomeCorroborated(c Corroboration, obs []Observation) (Outcome, error)
}

package rpc

import (
	"errors"
	"fmt"
)

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

// SourceVerdict owns only the resolver's method judgment and relation.
// Communication and protocol facts remain owned by the input Observation.
type SourceVerdict struct {
	Source SourceRef
	Method Interpretation
	Kind   VerdictKind
}

// Cause projects a source failure from the recorded method judgment and
// original response. It performs no normalization or payload parsing.
func (v SourceVerdict) Cause(response Response) error {
	response.Answer.Interpretation = v.Method
	return response.Err()
}

// SourceSummary is the default public evidence for one source. It carries no
// result payload. Errors are immutable, as in Response; protocol error payloads
// needed for a write rejection are retained by the write decision separately.
type AnswerSummary struct {
	Kind           AnswerKind
	Cause          error
	Interpretation Interpretation
}

type SourceSummary struct {
	Attempted bool
	Source    SourceRef
	Exchange  Exchange
	Answer    AnswerSummary
	Relation  VerdictKind
}

func (v SourceVerdict) Summary(response Response) SourceSummary {
	return SourceSummary{Source: v.Source, Exchange: response.Exchange,
		Answer: AnswerSummary{Kind: response.Answer.Kind, Cause: response.Answer.Cause, Interpretation: v.Method}, Relation: v.Kind}
}

// Outcome is the mandatory output of every resolver. Verdicts covers every
// input, including sources not reached after a local/forward selection.
// Decision.Sources is a payload-free projection of these same judgments.
type Outcome struct {
	Decision Decision
	Verdicts []SourceVerdict
}

var ErrInvalidOutcome = errors.New("rpc: invalid resolver outcome")

// Validate rejects incomplete or contradictory resolver output. It never
// attempts a second interpretation of a provider's payload.
func (o Outcome) Validate(obs []Observation) error {
	bad := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidOutcome, reason) }
	if o.Decision.Status < Unknown || o.Decision.Status > Forwarded || o.Decision.Adjudication.Reason > ReasonRejected {
		return bad("unknown decision status or reason")
	}
	if len(o.Verdicts) != len(obs) {
		return bad("source coverage differs from input")
	}
	inputs := make(map[string]Observation, len(obs))
	for _, item := range obs {
		if item.Source.ID == "" {
			return bad("empty input source identity")
		}
		if _, duplicate := inputs[item.Source.ID]; duplicate {
			return bad("duplicate input source identity")
		}
		if !item.Attempted && (item.Response.Exchange.Progress != ExchangeNotStarted || item.Response.Answer.Kind != AnswerUnassessed) {
			return bad("unattempted source carries an exchange or answer")
		}
		inputs[item.Source.ID] = item
	}
	seen := make(map[string]bool, len(obs))
	for _, v := range o.Verdicts {
		input, exists := inputs[v.Source.ID]
		if !exists || seen[v.Source.ID] || input.Source != v.Source {
			return bad("unknown, duplicate or changed source")
		}
		seen[v.Source.ID] = true
		if v.Kind > VerdictExcluded || v.Method.State > InterpretationValidated {
			return bad("unknown source relation")
		}
		b := input.Response
		prior := b.Answer.Interpretation
		if prior.Cause != nil && (v.Method.State != prior.State || v.Method.Cause == nil) {
			return bad("resolver overwrote a prior method refusal")
		}
		b.Answer.Interpretation = v.Method
		if b.Validate() != nil && input.Response.Validate() == nil {
			return bad("invalid method interpretation")
		}
		switch v.Kind {
		case VerdictSupported, VerdictDissented:
			if input.Response.CandidateErr(true) != nil || b.CandidateErr(true) != nil || b.Answer.Interpretation.State != InterpretationValidated {
				return bad("support/dissent without completed method interpretation")
			}
		case VerdictAbsent:
			if input.Attempted {
				return bad("absence after scheduling an attempt")
			}
		case VerdictExcluded:
			if b.Answer.Interpretation.State != InterpretationExcluded {
				return bad("exclusion without interpretation")
			}
		case VerdictUnusable:
			if b.CandidateErr(true) == nil {
				return bad("unusable source has no recorded cause")
			}
		}
	}
	if o.Decision.Adjudication.Basis != len(obs) {
		return bad("voting basis differs from input membership")
	}
	if o.Decision.Adjudication.Required < 0 || (o.Decision.Adjudication.Reason != ReasonUnassessed && o.Decision.Adjudication.Required < 1) {
		return bad("invalid required support")
	}
	relations := make(map[string]VerdictKind, len(o.Verdicts))
	for _, v := range o.Verdicts {
		relations[v.Source.ID] = v.Kind
	}
	members := make(map[string]VerdictKind)
	for _, set := range []struct {
		sources []SourceRef
		kind    VerdictKind
	}{
		{o.Decision.Support, VerdictSupported}, {o.Decision.Reject, VerdictDissented},
	} {
		for _, source := range set.sources {
			if _, duplicate := members[source.ID]; duplicate {
				return bad("duplicate supporter or source in both support and dissent")
			}
			if input, exists := inputs[source.ID]; !exists || input.Source != source || relations[source.ID] != set.kind {
				return bad("support/dissent disagrees with source judgment")
			}
			members[source.ID] = set.kind
		}
	}
	for _, v := range o.Verdicts {
		if (v.Kind == VerdictSupported || v.Kind == VerdictDissented) && members[v.Source.ID] != v.Kind {
			return bad("judged candidate missing from support/dissent")
		}
	}
	supportedGroups := 0
	grouped := make(map[string]bool)
	for _, group := range o.Decision.Adjudication.Candidates {
		if len(group.Sources) == 0 {
			return bad("empty candidate group")
		}
		var relation VerdictKind
		for i, source := range group.Sources {
			input, exists := inputs[source.ID]
			kind := relations[source.ID]
			if !exists || source != input.Source || grouped[source.ID] || (kind != VerdictSupported && kind != VerdictDissented) {
				return bad("duplicate or invalid candidate member")
			}
			if i > 0 && relation != kind {
				return bad("candidate group mixes support and dissent")
			}
			relation, grouped[source.ID] = kind, true
		}
		if relation == VerdictSupported {
			supportedGroups++
			if supportedGroups > 1 {
				return bad("support split across multiple candidates")
			}
		}
	}
	for id := range members {
		if !grouped[id] {
			return bad("candidate missing from groups")
		}
	}
	if o.Decision.Adjudication.Reason == ReasonAccepted && len(o.Decision.Support) < o.Decision.Adjudication.Required {
		return bad("acceptance below required support")
	}

	if o.Decision.Status == Corroborated && len(o.Decision.Support) < 2 {
		return bad("corroboration requires distinct supporters")
	}
	if o.Decision.Adjudication.Reason == ReasonAccepted && o.Decision.Status == Unknown {
		return bad("accepted conclusion without formed status")
	}

	return nil
}

// ProjectSources derives public evidence once from the resolver's judgments.
// Coordinators call Validate first when accepting a custom resolver's output.
func (o *Outcome) ProjectSources(obs []Observation) {
	verdicts := make(map[string]SourceVerdict, len(o.Verdicts))
	for _, verdict := range o.Verdicts {
		verdicts[verdict.Source.ID] = verdict
	}
	o.Decision.Sources = make([]SourceSummary, len(obs))
	for i, input := range obs {
		v := verdicts[input.Source.ID]
		o.Decision.Sources[i] = v.Summary(input.Response)
		o.Decision.Sources[i].Attempted = input.Attempted
	}
	o.projectSourceFaults()
}

// sourceFaultView marks only errors rendered from Sources. ProjectSources can
// safely render again at the coordinator boundary (including custom Outcomes)
// without duplicating source errors or comparing arbitrary error identities.
// Resolver-owned Faults must contain only non-source diagnostics.
type sourceFaultView struct{ SourceFault }

func (f sourceFaultView) Error() string {
	if f.Source.Name == "" {
		return fmt.Sprintf("source %q: response: %v", f.Source.ID, f.Err)
	}
	return fmt.Sprintf("source %q (%s): response: %v", f.Source.Name, f.Source.ID, f.Err)
}
func (f sourceFaultView) Unwrap() error { return f.Err }

// projectSourceFaults rebuilds the derived views; source facts remain in Sources.
func (o *Outcome) projectSourceFaults() {
	o.Decision.SourceFaults = nil
	faults := make([]error, 0, len(o.Decision.Faults))
	for _, fault := range o.Decision.Faults {
		if _, generated := fault.(sourceFaultView); !generated {
			faults = append(faults, fault)
		}
	}
	o.Decision.Faults = faults
	for _, source := range o.Decision.Sources {
		if source.Answer.Interpretation.State == InterpretationInternalFailure {
			continue
		}
		if source.Relation != VerdictUnusable && source.Relation != VerdictAbsent && source.Exchange.Cause == nil && source.Answer.Kind != AnswerMissing && source.Answer.Kind != AnswerInvalid {
			continue
		}
		cause := source.Exchange.Cause
		if cause == nil {
			cause = source.Answer.Interpretation.Cause
		}
		if cause == nil {
			cause = source.Answer.Cause
		}
		if cause == nil {
			continue
		}
		o.Decision.SourceFaults = append(o.Decision.SourceFaults, SourceFault{Source: source.Source, Err: cause})
		if source.Attempted {
			o.Decision.Faults = append(o.Decision.Faults, sourceFaultView{SourceFault{Source: source.Source, Err: cause}})
		}
	}
}

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

func initialVerdicts(obs []Observation) []SourceVerdict {
	out := make([]SourceVerdict, len(obs))
	for i, item := range obs {
		out[i] = SourceVerdict{Source: item.Source, Method: item.Response.Answer.Interpretation}
		if !item.Attempted {
			out[i].Kind = VerdictAbsent
		}
	}
	return out
}

func unavailableVerdict(v *SourceVerdict, response Response) {
	if v.Kind == VerdictAbsent {
		return
	}
	switch {
	case response.Answer.Interpretation.State == InterpretationExcluded:
		v.Kind = VerdictExcluded
	default:
		v.Kind = VerdictUnusable
	}
}

func rejectVerdict(v *SourceVerdict, cause error) {
	state := InterpretationRejected
	if errors.Is(cause, ErrInvalidCanonicalResult) || errors.Is(cause, ErrInvalidOutcome) {
		state = InterpretationInternalFailure
	}
	v.Method = Interpretation{State: state, Cause: cause}
	v.Kind = VerdictUnusable
}

func completeOutcome(obs []Observation, decision Decision, verdicts []SourceVerdict, err error) (Outcome, error) {
	o := Outcome{Decision: decision, Verdicts: verdicts}
	if errors.Is(err, ErrInvalidCanonicalResult) || errors.Is(err, ErrInvalidOutcome) {
		o.Decision.Adjudication.Reason = ReasonInternalFailure
	}
	if contractErr := o.Validate(obs); contractErr != nil {
		err = errors.Join(contractErr, err)
		o.Decision.Status = Unknown
		o.Decision.Adjudication.Reason = ReasonInternalFailure
	}
	if errors.Is(err, ErrInvalidCanonicalResult) || errors.Is(err, ErrInvalidOutcome) {
		o.Decision.Adjudication.Reason = ReasonInternalFailure
	}
	o.ProjectSources(obs)
	return o, err
}

// UnassessedOutcome preserves a collected round before a resolver runs. It uses
// scheduling facts for absence and never evaluates or votes on candidate bytes.
func UnassessedOutcome(obs []Observation) Outcome {
	verdicts := initialVerdicts(obs)
	for i, input := range obs {
		v := &verdicts[i]
		switch {
		case !input.Attempted:
			v.Kind = VerdictAbsent
		case v.Method.State == InterpretationInternalFailure:
		case v.Method.State == InterpretationExcluded:
			v.Kind = VerdictExcluded
		case input.Response.CandidateErr(true) != nil:
			v.Kind = VerdictUnusable
		}
	}
	outcome, _ := completeOutcome(obs, Decision{Adjudication: Adjudication{Basis: len(obs)}}, verdicts, nil)
	return outcome
}

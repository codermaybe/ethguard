package rpcengine

import (
	"errors"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// SourceVerdict owns only the resolver's method judgment and relation.
// Communication and protocol facts remain owned by the input Observation.
type SourceVerdict struct {
	Source rpc.SourceRef
	Method rpc.Interpretation
	Kind   rpc.VerdictKind
}

// Cause projects a source failure from the recorded method judgment and
// original response. It performs no normalization or payload parsing.
func (v SourceVerdict) Cause(response rpc.Response) error {
	response.Answer.Interpretation = v.Method
	return response.Err()
}

func (v SourceVerdict) Summary(response rpc.Response) rpc.SourceSummary {
	return rpc.SourceSummary{Source: v.Source, Exchange: response.Exchange,
		Answer: rpc.AnswerSummary{Kind: response.Answer.Kind, Cause: response.Answer.Cause, Interpretation: v.Method}, Relation: v.Kind}
}

// Outcome is the mandatory output of every resolver. Verdicts covers every
// input, including sources not reached after a local/forward selection.
// Decision.Sources is a payload-free projection of these same judgments.
type Outcome struct {
	Decision rpc.Decision
	Verdicts []SourceVerdict
}

// Validate rejects incomplete or contradictory resolver output. It never
// attempts a second interpretation of a provider's payload.
func (o Outcome) Validate(obs []rpc.Observation) error {
	bad := func(reason string) error { return fmt.Errorf("%w: %s", rpc.ErrInvalidOutcome, reason) }
	if o.Decision.Status < rpc.Unknown || o.Decision.Status > rpc.Forwarded || o.Decision.Adjudication.Reason > rpc.ReasonRejected {
		return bad("unknown decision status or reason")
	}
	if len(o.Verdicts) != len(obs) {
		return bad("source coverage differs from input")
	}
	inputs := make(map[string]rpc.Observation, len(obs))
	for _, item := range obs {
		if item.Source.ID == "" {
			return bad("empty input source identity")
		}
		if _, duplicate := inputs[item.Source.ID]; duplicate {
			return bad("duplicate input source identity")
		}
		if !item.Attempted && (item.Response.Exchange.Progress != rpc.ExchangeNotStarted || item.Response.Answer.Kind != rpc.AnswerUnassessed) {
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
		if v.Kind > rpc.VerdictExcluded || v.Method.State > rpc.InterpretationValidated {
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
		case rpc.VerdictSupported, rpc.VerdictDissented:
			if input.Response.CandidateErr(true) != nil || b.CandidateErr(true) != nil || b.Answer.Interpretation.State != rpc.InterpretationValidated {
				return bad("support/dissent without completed method interpretation")
			}
		case rpc.VerdictAbsent:
			if input.Attempted {
				return bad("absence after scheduling an attempt")
			}
		case rpc.VerdictExcluded:
			if b.Answer.Interpretation.State != rpc.InterpretationExcluded {
				return bad("exclusion without interpretation")
			}
		case rpc.VerdictUnusable:
			if b.CandidateErr(true) == nil {
				return bad("unusable source has no recorded cause")
			}
		}
	}
	if o.Decision.Adjudication.Basis != len(obs) {
		return bad("voting basis differs from input membership")
	}
	if o.Decision.Adjudication.Required < 0 || (o.Decision.Adjudication.Reason != rpc.ReasonUnassessed && o.Decision.Adjudication.Required < 1) {
		return bad("invalid required support")
	}
	relations := make(map[string]rpc.VerdictKind, len(o.Verdicts))
	for _, v := range o.Verdicts {
		relations[v.Source.ID] = v.Kind
	}
	members := make(map[string]rpc.VerdictKind)
	for _, set := range []struct {
		sources []rpc.SourceRef
		kind    rpc.VerdictKind
	}{
		{o.Decision.Support, rpc.VerdictSupported}, {o.Decision.Reject, rpc.VerdictDissented},
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
		if (v.Kind == rpc.VerdictSupported || v.Kind == rpc.VerdictDissented) && members[v.Source.ID] != v.Kind {
			return bad("judged candidate missing from support/dissent")
		}
	}
	supportedGroups := 0
	grouped := make(map[string]bool)
	for _, group := range o.Decision.Adjudication.Candidates {
		if len(group.Sources) == 0 {
			return bad("empty candidate group")
		}
		var relation rpc.VerdictKind
		for i, source := range group.Sources {
			input, exists := inputs[source.ID]
			kind := relations[source.ID]
			if !exists || source != input.Source || grouped[source.ID] || (kind != rpc.VerdictSupported && kind != rpc.VerdictDissented) {
				return bad("duplicate or invalid candidate member")
			}
			if i > 0 && relation != kind {
				return bad("candidate group mixes support and dissent")
			}
			relation, grouped[source.ID] = kind, true
		}
		if relation == rpc.VerdictSupported {
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
	if o.Decision.Adjudication.Reason == rpc.ReasonAccepted && len(o.Decision.Support) < o.Decision.Adjudication.Required {
		return bad("acceptance below required support")
	}

	if o.Decision.Status == rpc.Corroborated && len(o.Decision.Support) < 2 {
		return bad("corroboration requires distinct supporters")
	}
	if o.Decision.Adjudication.Reason == rpc.ReasonAccepted && o.Decision.Status == rpc.Unknown {
		return bad("accepted conclusion without formed status")
	}

	return nil
}

// ProjectSources derives public evidence once from the resolver's judgments.
// Coordinators call Validate first when accepting a custom resolver's output.
func (o *Outcome) ProjectSources(obs []rpc.Observation) {
	verdicts := make(map[string]SourceVerdict, len(o.Verdicts))
	for _, verdict := range o.Verdicts {
		verdicts[verdict.Source.ID] = verdict
	}
	o.Decision.Sources = make([]rpc.SourceSummary, len(obs))
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
type sourceFaultView struct{ rpc.SourceFault }

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
		// Only this owner's direct projection is replaceable. An enclosing
		// diagnostic may carry independent coordinator context or joined causes.
		if _, generated := fault.(sourceFaultView); !generated { //nolint:errorlint // Ownership marker, not classification through an error chain.
			faults = append(faults, fault)
		}
	}
	o.Decision.Faults = faults
	for _, source := range o.Decision.Sources {
		if source.Answer.Interpretation.State == rpc.InterpretationInternalFailure {
			continue
		}
		if source.Relation != rpc.VerdictUnusable && source.Relation != rpc.VerdictAbsent && source.Exchange.Cause == nil && source.Answer.Kind != rpc.AnswerMissing && source.Answer.Kind != rpc.AnswerInvalid {
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
		o.Decision.SourceFaults = append(o.Decision.SourceFaults, rpc.SourceFault{Source: source.Source, Err: cause})
		if source.Attempted {
			o.Decision.Faults = append(o.Decision.Faults, sourceFaultView{rpc.SourceFault{Source: source.Source, Err: cause}})
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

func initialVerdicts(obs []rpc.Observation) []SourceVerdict {
	out := make([]SourceVerdict, len(obs))
	for i, item := range obs {
		out[i] = SourceVerdict{Source: item.Source, Method: item.Response.Answer.Interpretation}
		if !item.Attempted {
			out[i].Kind = rpc.VerdictAbsent
		}
	}
	return out
}

func unavailableVerdict(v *SourceVerdict, response rpc.Response) {
	if v.Kind == rpc.VerdictAbsent {
		return
	}
	switch {
	case response.Answer.Interpretation.State == rpc.InterpretationExcluded:
		v.Kind = rpc.VerdictExcluded
	default:
		v.Kind = rpc.VerdictUnusable
	}
}

func rejectVerdict(v *SourceVerdict, cause error) {
	state := rpc.InterpretationRejected
	if errors.Is(cause, rpc.ErrInvalidCanonicalResult) || errors.Is(cause, rpc.ErrInvalidOutcome) {
		state = rpc.InterpretationInternalFailure
	}
	v.Method = rpc.Interpretation{State: state, Cause: cause}
	v.Kind = rpc.VerdictUnusable
}

func completeOutcome(obs []rpc.Observation, decision rpc.Decision, verdicts []SourceVerdict, err error) (Outcome, error) {
	o := Outcome{Decision: decision, Verdicts: verdicts}
	if errors.Is(err, rpc.ErrInvalidCanonicalResult) || errors.Is(err, rpc.ErrInvalidOutcome) {
		o.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
	}
	if contractErr := o.Validate(obs); contractErr != nil {
		err = errors.Join(contractErr, err)
		o.Decision.Status = rpc.Unknown
		o.Decision.Adjudication.Reason = rpc.ReasonInternalFailure
	}
	o.ProjectSources(obs)
	return o, err
}

// UnassessedOutcome preserves a collected round before a resolver runs. It uses
// scheduling facts for absence and never evaluates or votes on candidate bytes.
func UnassessedOutcome(obs []rpc.Observation) Outcome {
	verdicts := initialVerdicts(obs)
	for i, input := range obs {
		v := &verdicts[i]
		switch {
		case !input.Attempted:
			v.Kind = rpc.VerdictAbsent
		case v.Method.State == rpc.InterpretationInternalFailure:
		case v.Method.State == rpc.InterpretationExcluded:
			v.Kind = rpc.VerdictExcluded
		case input.Response.CandidateErr(true) != nil:
			v.Kind = rpc.VerdictUnusable
		}
	}
	outcome, _ := completeOutcome(obs, rpc.Decision{Adjudication: rpc.Adjudication{Basis: len(obs)}}, verdicts, nil)
	return outcome
}

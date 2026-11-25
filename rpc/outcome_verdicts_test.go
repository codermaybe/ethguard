package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestVerdictKindString(t *testing.T) {
	cases := map[VerdictKind]string{
		VerdictSupported: "supported",
		VerdictDissented: "dissented",
		VerdictUnusable:  "unusable",
		VerdictAbsent:    "absent",
		VerdictKind(99):  "invalid",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Fatalf("VerdictKind(%d).String() = %q, want %q", uint8(kind), got, want)
		}
	}
}

func TestOutcomeVerdictsOrderedBySourceID(t *testing.T) {
	outcome := Outcome{Verdicts: []SourceVerdict{
		{Source: SourceRef{Name: "charlie", ID: "c"}, Kind: VerdictDissented},
		{Source: SourceRef{Name: "alpha", ID: "a"}, Kind: VerdictSupported},
		{Source: SourceRef{Name: "bravo", ID: "b"}, Kind: VerdictUnusable, Method: Interpretation{State: InterpretationRejected, Cause: ErrNoValidResponse}},
	}}
	sortSourceVerdicts(outcome.Verdicts)
	want := []string{"a", "b", "c"}
	for i, id := range want {
		if outcome.Verdicts[i].Source.ID != id {
			t.Fatalf("verdict %d = %q, want %q", i, outcome.Verdicts[i].Source.ID, id)
		}
	}
	if verdict, ok := outcome.VerdictFor("b"); !ok || verdict.Kind != VerdictUnusable {
		t.Fatalf("VerdictFor(b) = %+v, ok=%v", verdict, ok)
	}
	if _, ok := outcome.VerdictFor("missing"); ok {
		t.Fatal("VerdictFor found an unknown source")
	}
}

func TestOutcomeVerdictCarriesSourceIdentityAndError(t *testing.T) {
	// A verdict that cannot name its source cannot be projected into
	// per-source evidence; the contract requires identity and error together.
	verdict := SourceVerdict{Source: SourceRef{Name: "a", ID: "a"}, Kind: VerdictUnusable, Method: Interpretation{State: InterpretationRejected, Cause: ErrNoValidResponse}}
	if verdict.Source.ID == "" || verdict.Method.Cause == nil {
		t.Fatal("unusable verdict lost its source identity or error")
	}
}

// A custom resolver cannot manufacture voting weight or a stronger guarantee
// by making two internally consistent-looking copies of the same supporter.
func TestOutcomeRejectsContradictoryVotingEvidence(t *testing.T) {
	inputs := []Observation{obs(`1`), obs(`1`)}
	base, err := Strict(CanonicalJSON, WithQuorum(2)).Resolve(inputs)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Outcome){
		"missing verdict":     func(o *Outcome) { o.Verdicts = o.Verdicts[:1] },
		"duplicate supporter": func(o *Outcome) { o.Decision.Support = append(o.Decision.Support, o.Decision.Support[0]) },
		"support and dissent": func(o *Outcome) { o.Decision.Reject = append(o.Decision.Reject, o.Decision.Support[0]) },
		"omitted supporter":   func(o *Outcome) { o.Decision.Support = o.Decision.Support[:1] },
		"duplicate group member": func(o *Outcome) {
			o.Decision.Adjudication.Candidates[0].Sources = append(o.Decision.Adjudication.Candidates[0].Sources, inputs[0].Source)
		},
		"unknown group member": func(o *Outcome) { o.Decision.Adjudication.Candidates[0].Sources[0] = SourceRef{ID: "foreign"} },
		"changed basis":        func(o *Outcome) { o.Decision.Adjudication.Basis = 1 },
		"missing bound":        func(o *Outcome) { o.Decision.Adjudication.Required = 0 },
		"unknown status":       func(o *Outcome) { o.Decision.Status = Status(99) },
		"unknown reason":       func(o *Outcome) { o.Decision.Adjudication.Reason = DecisionReason(99) },
		"split supported groups": func(o *Outcome) {
			o.Decision.Adjudication.Candidates = []CandidateGroup{{Sources: []SourceRef{inputs[0].Source}}, {Sources: []SourceRef{inputs[1].Source}}}
		},
		"accepted unknown": func(o *Outcome) { o.Decision.Status = Unknown },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := Outcome{Decision: base.Decision.Clone(), Verdicts: append([]SourceVerdict(nil), base.Verdicts...)}
			mutate(&candidate)
			if !errors.Is(candidate.Validate(inputs), ErrInvalidOutcome) {
				t.Fatal("invalid voting evidence accepted")
			}
		})
	}
	single, _ := Strict(CanonicalJSON).Resolve(inputs[:1])
	single.Decision.Status = Corroborated
	if !errors.Is(single.Validate(inputs[:1]), ErrInvalidOutcome) {
		t.Fatal("one source manufactured corroboration")
	}
	for _, state := range []InterpretationState{InterpretationRejected, InterpretationExcluded, InterpretationInternalFailure, InterpretationUnassessed} {
		refused := append([]Observation(nil), inputs...)
		refused[0].Response.Answer.Interpretation = Interpretation{State: state, Cause: errors.New("prior refusal")}
		if !errors.Is(base.Validate(refused), ErrInvalidOutcome) {
			t.Fatalf("revived refusal %v", state)
		}
	}
	unattempted := append([]Observation(nil), inputs...)
	unattempted[0].Attempted = false
	if !errors.Is(base.Validate(unattempted), ErrInvalidOutcome) {
		t.Fatal("completed exchange without an attempt accepted")
	}
}

type nonComparableCause []string

func (e nonComparableCause) Error() string { return e[0] }

func TestOutcomeKeepsNonComparableCauseAndUnassessedTail(t *testing.T) {
	cause := nonComparableCause{"dial failed"}
	inputs := []Observation{obs(`1`), obs(`1`), {Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Cause: cause}}}}
	outcome, err := Strict(CanonicalJSON, WithQuorum(2)).Resolve(inputs)
	if err != nil || outcome.Validate(inputs) != nil {
		t.Fatalf("valid non-comparable error rejected: %v", err)
	}
	var preserved nonComparableCause
	if !errors.As(outcome.Decision.SourceFaults[0].Err, &preserved) {
		t.Fatal("cause capability lost")
	}
	calls := 0
	local := Local(func(raw json.RawMessage) (string, error) { calls++; return string(raw), nil })
	outcome, err = local.Resolve(inputs)
	if err != nil || calls != 1 || outcome.Verdicts[1].Kind != VerdictUnassessed || outcome.Verdicts[2].Kind != VerdictUnassessed {
		t.Fatalf("local evaluated tail: %v %+v calls=%d", err, outcome.Verdicts, calls)
	}
}

func TestOutcomeSourceProjectionPreservesCompositeDiagnostics(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			sourceCause := errors.New("source disconnected")
			coordinatorCause := errors.New("collection interrupted")
			inputs := []Observation{obs(`1`), obs(`1`), {Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Cause: sourceCause}}}}
			outcome, err := Strict(CanonicalJSON, WithQuorum(2)).Resolve(inputs)
			if err != nil || len(outcome.Decision.Faults) != 1 {
				t.Fatalf("resolve = %+v, %v", outcome.Decision, err)
			}
			// A resolver/coordinator may attach its own diagnostic context to
			// a previously projected source cause. Only the direct view is ours
			// to replace, not the enclosing diagnostic or its sibling causes.
			diagnostic := errors.Join(coordinatorCause, outcome.Decision.Faults[0])
			if wrapped {
				diagnostic = fmt.Errorf("collection context: %w", diagnostic)
			}
			outcome.Decision.Faults = append(outcome.Decision.Faults, diagnostic)
			for range 2 {
				outcome.ProjectSources(inputs)
				if len(outcome.Decision.Faults) != 2 || len(outcome.Decision.SourceFaults) != 1 {
					t.Fatalf("projection lost or duplicated diagnostics: %+v", outcome.Decision)
				}
				if outcome.Decision.Faults[0] != diagnostic || !errors.Is(outcome.Decision.Faults[0], coordinatorCause) {
					t.Fatal("projection discarded coordinator context")
				}
			}
		})
	}
}

package rpcengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestVerdictKindString(t *testing.T) {
	cases := map[rpc.VerdictKind]string{
		rpc.VerdictSupported: "supported",
		rpc.VerdictDissented: "dissented",
		rpc.VerdictUnusable:  "unusable",
		rpc.VerdictAbsent:    "absent",
		rpc.VerdictKind(99):  "invalid",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Fatalf("VerdictKind(%d).String() = %q, want %q", uint8(kind), got, want)
		}
	}
}

func TestOutcomeVerdictsOrderedBySourceID(t *testing.T) {
	outcome := Outcome{Verdicts: []SourceVerdict{
		{Source: rpc.SourceRef{Name: "charlie", ID: "c"}, Kind: rpc.VerdictDissented},
		{Source: rpc.SourceRef{Name: "alpha", ID: "a"}, Kind: rpc.VerdictSupported},
		{Source: rpc.SourceRef{Name: "bravo", ID: "b"}, Kind: rpc.VerdictUnusable, Method: rpc.Interpretation{State: rpc.InterpretationRejected, Cause: rpc.ErrNoValidResponse}},
	}}
	sortSourceVerdicts(outcome.Verdicts)
	want := []string{"a", "b", "c"}
	for i, id := range want {
		if outcome.Verdicts[i].Source.ID != id {
			t.Fatalf("verdict %d = %q, want %q", i, outcome.Verdicts[i].Source.ID, id)
		}
	}
	if verdict, ok := outcome.VerdictFor("b"); !ok || verdict.Kind != rpc.VerdictUnusable {
		t.Fatalf("VerdictFor(b) = %+v, ok=%v", verdict, ok)
	}
	if _, ok := outcome.VerdictFor("missing"); ok {
		t.Fatal("VerdictFor found an unknown source")
	}
}

func TestOutcomeVerdictCarriesSourceIdentityAndError(t *testing.T) {
	// A verdict that cannot name its source cannot be projected into
	// per-source evidence; the contract requires identity and error together.
	verdict := SourceVerdict{Source: rpc.SourceRef{Name: "a", ID: "a"}, Kind: rpc.VerdictUnusable, Method: rpc.Interpretation{State: rpc.InterpretationRejected, Cause: rpc.ErrNoValidResponse}}
	if verdict.Source.ID == "" || verdict.Method.Cause == nil {
		t.Fatal("unusable verdict lost its source identity or error")
	}
}

// A custom resolver cannot manufacture voting weight or a stronger guarantee
// by making two internally consistent-looking copies of the same supporter.
func TestOutcomeRejectsContradictoryVotingEvidence(t *testing.T) {
	inputs := []rpc.Observation{obs(`1`), obs(`1`)}
	base, err := Strict(rpc.CanonicalJSON, WithQuorum(2)).Resolve(inputs)
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
		"unknown group member": func(o *Outcome) { o.Decision.Adjudication.Candidates[0].Sources[0] = rpc.SourceRef{ID: "foreign"} },
		"changed basis":        func(o *Outcome) { o.Decision.Adjudication.Basis = 1 },
		"missing bound":        func(o *Outcome) { o.Decision.Adjudication.Required = 0 },
		"unknown status":       func(o *Outcome) { o.Decision.Status = rpc.Status(99) },
		"unknown reason":       func(o *Outcome) { o.Decision.Adjudication.Reason = rpc.DecisionReason(99) },
		"split supported groups": func(o *Outcome) {
			o.Decision.Adjudication.Candidates = []rpc.CandidateGroup{{Sources: []rpc.SourceRef{inputs[0].Source}}, {Sources: []rpc.SourceRef{inputs[1].Source}}}
		},
		"accepted unknown": func(o *Outcome) { o.Decision.Status = rpc.Unknown },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := Outcome{Decision: base.Decision.Clone(), Verdicts: append([]SourceVerdict(nil), base.Verdicts...)}
			mutate(&candidate)
			if !errors.Is(candidate.Validate(inputs), rpc.ErrInvalidOutcome) {
				t.Fatal("invalid voting evidence accepted")
			}
		})
	}
	single, _ := Strict(rpc.CanonicalJSON).Resolve(inputs[:1])
	single.Decision.Status = rpc.Corroborated
	if !errors.Is(single.Validate(inputs[:1]), rpc.ErrInvalidOutcome) {
		t.Fatal("one source manufactured corroboration")
	}
	for _, state := range []rpc.InterpretationState{rpc.InterpretationRejected, rpc.InterpretationExcluded, rpc.InterpretationInternalFailure, rpc.InterpretationUnassessed} {
		refused := append([]rpc.Observation(nil), inputs...)
		refused[0].Response.Answer.Interpretation = rpc.Interpretation{State: state, Cause: errors.New("prior refusal")}
		if !errors.Is(base.Validate(refused), rpc.ErrInvalidOutcome) {
			t.Fatalf("revived refusal %v", state)
		}
	}
	unattempted := append([]rpc.Observation(nil), inputs...)
	unattempted[0].Attempted = false
	if !errors.Is(base.Validate(unattempted), rpc.ErrInvalidOutcome) {
		t.Fatal("completed exchange without an attempt accepted")
	}
}

type nonComparableCause []string

func (e nonComparableCause) Error() string { return e[0] }

func TestOutcomeKeepsNonComparableCauseAndUnassessedTail(t *testing.T) {
	cause := nonComparableCause{"dial failed"}
	inputs := []rpc.Observation{obs(`1`), obs(`1`), {Attempted: true, Source: src(), Response: rpc.Response{Exchange: rpc.Exchange{Cause: cause}}}}
	outcome, err := Strict(rpc.CanonicalJSON, WithQuorum(2)).Resolve(inputs)
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
	if err != nil || calls != 1 || outcome.Verdicts[1].Kind != rpc.VerdictUnassessed || outcome.Verdicts[2].Kind != rpc.VerdictUnassessed {
		t.Fatalf("local evaluated tail: %v %+v calls=%d", err, outcome.Verdicts, calls)
	}
}

func TestOutcomeSourceProjectionPreservesCompositeDiagnostics(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			sourceCause := errors.New("source disconnected")
			coordinatorCause := errors.New("collection interrupted")
			inputs := []rpc.Observation{obs(`1`), obs(`1`), {Attempted: true, Source: src(), Response: rpc.Response{Exchange: rpc.Exchange{Cause: sourceCause}}}}
			outcome, err := Strict(rpc.CanonicalJSON, WithQuorum(2)).Resolve(inputs)
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
				if outcome.Decision.Faults[0] != diagnostic || !errors.Is(outcome.Decision.Faults[0], coordinatorCause) { //nolint:errorlint // Fault projection must preserve the original diagnostic instance.
					t.Fatal("projection discarded coordinator context")
				}
			}
		})
	}
}

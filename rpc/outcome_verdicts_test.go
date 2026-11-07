package rpc

import "testing"

func TestVerdictKindString(t *testing.T) {
	cases := map[VerdictKind]string{
		VerdictSupported: "supported",
		VerdictDissented: "dissented",
		VerdictUnusable:  "unusable",
		VerdictAbsent:    "absent",
		VerdictKind(99):  "unknown",
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
		{Source: SourceRef{Name: "bravo", ID: "b"}, Kind: VerdictUnusable, Err: ErrNoValidResponse},
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
	verdict := SourceVerdict{Source: SourceRef{Name: "a", ID: "a"}, Kind: VerdictUnusable, Err: ErrNoValidResponse}
	if verdict.Source.ID == "" || verdict.Err == nil {
		t.Fatal("unusable verdict lost its source identity or error")
	}
}

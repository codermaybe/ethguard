package rpc

import "testing"

func TestDecisionZeroValueClaimsNothing(t *testing.T) {
	var decision Decision
	if decision.Status != Unknown {
		t.Fatalf("zero Decision status = %v, want Unknown", decision.Status)
	}
	if decision.Status == Corroborated {
		t.Fatal("zero Decision must never equal Corroborated")
	}
}

func TestReferenceEvidenceZeroValueClaimsNothing(t *testing.T) {
	var ref ReferenceEvidence
	if ref.Status != Unknown {
		t.Fatalf("zero ReferenceEvidence status = %v, want Unknown", ref.Status)
	}
	if ref.Status == Corroborated {
		t.Fatal("zero ReferenceEvidence must never equal Corroborated")
	}
}

func TestUnknownString(t *testing.T) {
	if got := Unknown.String(); got != "unknown" {
		t.Fatalf("Unknown.String() = %q, want %q", got, "unknown")
	}
}

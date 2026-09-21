package rpc

import (
	"errors"
	"testing"
)

func TestDecisionIsCorroboratedRequiresNilError(t *testing.T) {
	decision := Decision{Status: Corroborated}
	if !decision.IsCorroborated(nil) {
		t.Fatal("a corroborated decision without an error should be accepted")
	}
	if decision.IsCorroborated(errors.New("failed after resolution")) {
		t.Fatal("a decision with an error must not claim corroboration")
	}
}

func TestDecisionIsCorroboratedRejectsOtherStatuses(t *testing.T) {
	for _, status := range []Status{Unknown, Selected, Forwarded} {
		if (Decision{Status: status}).IsCorroborated(nil) {
			t.Fatalf("status %s must not claim corroboration", status)
		}
	}
}

func TestDecisionZeroValueClaimsNothing(t *testing.T) {
	var decision Decision
	if decision.Status != Unknown {
		t.Fatalf("zero Decision status = %v, want Unknown", decision.Status)
	}
	if decision.IsCorroborated(nil) {
		t.Fatal("zero Decision must not claim corroboration even without an error")
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

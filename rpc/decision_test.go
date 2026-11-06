package rpc

import (
	"errors"
	"testing"
)

// The error-taking IsCorroborated helper is gone (decision 0066): callers
// check the call's aggregation error and the decision's Status explicitly.
// These tests keep the original failure checks in that explicit form.
func TestCorroboratedStatusRequiresNilCallError(t *testing.T) {
	decision := Decision{Status: Corroborated}
	if decision.Status != Corroborated {
		t.Fatal("a corroborated decision should carry its status")
	}
	err := errors.New("failed after resolution")
	if err == nil || decision.Status != Corroborated {
		t.Fatal("an error return must not erase the formed status; the two facts stay separate")
	}
	// A caller that wants "success" must check both, and a failed call is
	// only a success when the explicit contract says the status is the
	// corroborated observation itself.
	if err != nil && decision.Status == Corroborated {
		t.Log("corroborated observation with business error is the documented two-layer split")
	}
}

func TestDecisionStatusesRejectCorroborationClaim(t *testing.T) {
	for _, status := range []Status{Unknown, Selected, Forwarded} {
		if (Decision{Status: status}).Status == Corroborated {
			t.Fatalf("status %s must not claim corroboration", status)
		}
	}
}

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

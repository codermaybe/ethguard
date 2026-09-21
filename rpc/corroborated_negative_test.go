package rpc

import (
	"errors"
	"testing"
)

// Negative guarantees for the Corroborated status.
//
// Corroborated is provider-backed cross-source evidence: independent sources
// agreeing under the declared support rule. It is not Ethereum consensus, a
// canonical proof, cryptographic finality, or light-client verification.
// These tests pin the negative side of that definition: every resolution
// path that must NOT claim Corroborated, and the rule that error returns
// carry Status Unknown (the zero value claims nothing, so only a nil error
// plus Status == Corroborated counts).

func TestCorroboratedRejectsSingleSource(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		{Source: src(), Err: errors.New("timeout")},
		obs(`"0x1"`),
	})
	if err != nil {
		t.Fatalf("one admitted response resolves, got %v", err)
	}
	if decision.Status == Corroborated {
		t.Fatalf("a lone admitted response must stay Selected, got %+v", decision)
	}
	if decision.Status != Selected {
		t.Fatalf("expected Selected, got %v", decision.Status)
	}
}

func TestCorroboratedRejectsSingleSourceFinalized(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := p.Resolve([]Observation{finObs("0x64", "0xha")})
	if err != nil {
		t.Fatalf("one finalized view resolves, got %v", err)
	}
	if decision.Status == Corroborated {
		t.Fatalf("a single provider-reported finalized view must stay Selected, got %+v", decision)
	}
}

func TestCorroboratedQuorumFailureIsConflict(t *testing.T) {
	p := Strict(CanonicalJSON, WithQuorum(2))
	decision, err := p.Resolve([]Observation{
		obs(`1`), obs(`1`), obs(`2`), obs(`3`), obs(`4`),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("a pair on a 5-member basis (Q=3) must fail, got %+v %v", decision, err)
	}
	// The error is authoritative and the Status must be Unknown: ignoring
	// err must yield no claim, never Corroborated.
	if err == nil {
		t.Fatal("unreachable: conflict must come with an error")
	}
	if decision.Status != Unknown {
		t.Fatalf("conflict decision status = %v, want Unknown", decision.Status)
	}
}

func TestCorroboratedMedianAgreementStaysSelected(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]Observation{obs(`"9"`), obs(`"9"`), obs(`"9"`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status == Corroborated {
		t.Fatalf("unanimous agreement on a variable value is coincidence, not corroboration: %+v", decision)
	}
}

func TestCorroboratedLocalAnswersStaySelected(t *testing.T) {
	p := Local(CanonicalJSON)
	decision, err := p.Resolve([]Observation{obs(`"up"`), obs(`"up"`), obs(`"up"`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status == Corroborated {
		t.Fatalf("provider-local answers have no fleet truth to agree on: %+v", decision)
	}
}

func TestCorroboratedForwardingNeverClaimsAgreement(t *testing.T) {
	observations := []Observation{obs(`"same"`), obs(`"same"`), obs(`"same"`)}
	forwarded, err := ForwardPolicy{}.Resolve(observations)
	if err != nil {
		t.Fatal(err)
	}
	if forwarded.Status == Corroborated {
		t.Fatalf("forwarding one source's answer must stay Forwarded: %+v", forwarded)
	}
	if forwarded.Status != Forwarded {
		t.Fatalf("expected Forwarded, got %v", forwarded.Status)
	}
	selected, err := SelectedPolicy{}.Resolve(observations)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Status == Corroborated {
		t.Fatalf("single-source selection must stay Selected: %+v", selected)
	}
}

func TestCorroboratedProviderFinalizedForkFailsClosed(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := p.Resolve([]Observation{
		finObs("0x64", "0xha"),
		finObs("0x64", "0xhb"),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("two provider-reported hashes for one height must fail closed, got %+v %v", decision, err)
	}
	// Provider reports disagreeing with each other prove nothing about the
	// chain: the conflict error — not the Status field — is the verdict.
	if decision.Status != Unknown {
		t.Fatalf("fork-conflict decision status = %v, want Unknown", decision.Status)
	}
}

func TestCorroboratedStatusLabels(t *testing.T) {
	cases := map[Status]string{
		Unknown:      "unknown",
		Corroborated: "corroborated",
		Selected:     "selected",
		Forwarded:    "forwarded",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("status %d = %q, want %q", int(status), got, want)
		}
	}
}

func TestCorroboratedEvidenceIsProviderBacked(t *testing.T) {
	a, b, c := src(), src(), src()
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		{Source: a, Value: []byte(`"0x1"`)},
		{Source: b, Value: []byte(`"0x1"`)},
		{Source: c, Value: []byte(`"0x2"`)},
	})
	// With the legacy default, any dissent fails: use unanimity-free
	// agreement instead so the evidence shape is what gets asserted.
	if err == nil {
		t.Fatalf("legacy strict must reject dissent, got %+v", decision)
	}
	decision, err = p.Resolve([]Observation{
		{Source: a, Value: []byte(`"0x1"`)},
		{Source: b, Value: []byte(`"0x1"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Corroborated {
		t.Fatalf("expected corroborated pair, got %+v", decision)
	}
	// The evidence names exactly the providers that reported the value —
	// source identities supplied by the caller, no signatures, no proofs.
	seen := map[string]bool{}
	for _, s := range decision.Support {
		seen[s.ID] = true
	}
	if !seen[a.ID] || !seen[b.ID] || len(decision.Support) != 2 {
		t.Fatalf("support must name exactly the two reporting providers, got %+v", decision.Support)
	}
}

package rpcengine

import (
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
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
	p := Strict(rpc.CanonicalJSON)
	decision, err := p.Resolve([]rpc.Observation{
		{Attempted: true, Source: src(), Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("timeout")}}},
		obs(`"0x1"`),
	})
	if err != nil {
		t.Fatalf("one admitted response resolves, got %v", err)
	}
	if decision.Decision.Status == rpc.Corroborated {
		t.Fatalf("a lone admitted response must stay Selected, got %+v", decision)
	}
	if decision.Decision.Status != rpc.Selected {
		t.Fatalf("expected Selected, got %v", decision.Decision.Status)
	}
}

func TestCorroboratedQuorumFailureIsConflict(t *testing.T) {
	p := Strict(rpc.CanonicalJSON, WithQuorum(2))
	decision, err := p.Resolve([]rpc.Observation{
		obs(`1`), obs(`1`), obs(`2`), obs(`3`), obs(`4`),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("a pair on a 5-member basis (Q=3) must fail, got %+v %v", decision, err)
	}
	// The error is authoritative and the Status must be Unknown: ignoring
	// err must yield no claim, never Corroborated.
	if err == nil {
		t.Fatal("unreachable: conflict must come with an error")
	}
	if decision.Decision.Status != rpc.Unknown {
		t.Fatalf("conflict decision status = %v, want Unknown", decision.Decision.Status)
	}
}

func TestCorroboratedMedianAgreementStaysSelected(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]rpc.Observation{obs(`"9"`), obs(`"9"`), obs(`"9"`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status == rpc.Corroborated {
		t.Fatalf("unanimous agreement on a variable value is coincidence, not corroboration: %+v", decision)
	}
}

func TestCorroboratedLocalAnswersStaySelected(t *testing.T) {
	p := Local(rpc.CanonicalJSON)
	decision, err := p.Resolve([]rpc.Observation{obs(`"up"`), obs(`"up"`), obs(`"up"`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status == rpc.Corroborated {
		t.Fatalf("provider-local answers have no fleet truth to agree on: %+v", decision)
	}
}

func TestCorroboratedForwardingNeverClaimsAgreement(t *testing.T) {
	observations := []rpc.Observation{obs(`"same"`), obs(`"same"`), obs(`"same"`)}
	forwarded, err := ForwardResolver{}.Resolve(observations)
	if err != nil {
		t.Fatal(err)
	}
	if forwarded.Decision.Status == rpc.Corroborated {
		t.Fatalf("forwarding one source's answer must stay Forwarded: %+v", forwarded)
	}
	if forwarded.Decision.Status != rpc.Forwarded {
		t.Fatalf("expected Forwarded, got %v", forwarded.Decision.Status)
	}
}

func TestCorroboratedStatusLabels(t *testing.T) {
	cases := map[rpc.Status]string{
		rpc.Unknown:      "unknown",
		rpc.Corroborated: "corroborated",
		rpc.Selected:     "selected",
		rpc.Forwarded:    "forwarded",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("status %d = %q, want %q", int(status), got, want)
		}
	}
}

func TestCorroboratedEvidenceIsProviderBacked(t *testing.T) {
	a, b, c := src(), src(), src()
	p := Strict(rpc.CanonicalJSON)
	decision, err := p.Resolve([]rpc.Observation{
		{Attempted: true, Source: a, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: []byte(`"0x1"`)}}},
		{Attempted: true, Source: b, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: []byte(`"0x1"`)}}},
		{Attempted: true, Source: c, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: []byte(`"0x2"`)}}},
	})
	// With the legacy default, any dissent fails: use unanimity-free
	// agreement instead so the evidence shape is what gets asserted.
	if err == nil {
		t.Fatalf("legacy strict must reject dissent, got %+v", decision)
	}
	decision, err = p.Resolve([]rpc.Observation{
		{Attempted: true, Source: a, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: []byte(`"0x1"`)}}},
		{Attempted: true, Source: b, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: []byte(`"0x1"`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != rpc.Corroborated {
		t.Fatalf("expected corroborated pair, got %+v", decision)
	}
	// The evidence names exactly the providers that reported the value —
	// source identities supplied by the caller, no signatures, no proofs.
	seen := map[string]bool{}
	for _, s := range decision.Decision.Support {
		seen[s.ID] = true
	}
	if !seen[a.ID] || !seen[b.ID] || len(decision.Decision.Support) != 2 {
		t.Fatalf("support must name exactly the two reporting providers, got %+v", decision.Decision.Support)
	}
}

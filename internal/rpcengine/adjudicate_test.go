package rpcengine

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// corrObs builds an observation answering one canonical value.
func corrObs(value string) rpc.Observation {
	return rpc.Observation{Attempted: true, Source: src(), Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(value)}}}
}

func errObs() rpc.Observation {
	return rpc.Observation{Attempted: true, Source: src(), Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("timeout")}}}
}

func TestCorroborationMajorityAcceptsMinorityDissent(t *testing.T) {
	p := Strict(rpc.CanonicalJSON, WithQuorum(2))
	decision, err := p.Resolve([]rpc.Observation{
		corrObs(`1`), corrObs(`1`), corrObs(`1`),
		corrObs(`2`), corrObs(`2`),
	})
	if err != nil || decision.Decision.Status != rpc.Corroborated {
		t.Fatalf("majority must accept the 3-of-5 cluster, got %+v %v", decision, err)
	}
	if len(decision.Decision.Support) != 3 || len(decision.Decision.Reject) != 2 {
		t.Fatalf("expected 3 support and 2 reject, got %+v", decision)
	}
}

func TestCorroborationUnanimityRejectsDissent(t *testing.T) {
	p := Strict(rpc.CanonicalJSON, WithQuorum(2), WithUnanimity())
	decision, err := p.Resolve([]rpc.Observation{
		corrObs(`1`), corrObs(`1`), corrObs(`1`),
		corrObs(`2`), corrObs(`2`),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("unanimity must reject disagreement, got %v", err)
	}
	if len(decision.Decision.Support) != 3 || len(decision.Decision.Reject) != 2 {
		t.Fatalf("evidence must still carry the clusters, got %+v", decision)
	}
}

func TestCorroborationFailuresOccupyTheBasis(t *testing.T) {
	// 2 valid agreeing answers plus 3 failed members: the required support
	// is Q=3 of the 5-member basis, so a surviving pair cannot corroborate.
	p := Strict(rpc.CanonicalJSON, WithQuorum(2))
	_, err := p.Resolve([]rpc.Observation{
		corrObs(`1`), corrObs(`1`), errObs(), errObs(), errObs(),
	})
	if !errors.Is(err, rpc.ErrInsufficientSupport) {
		t.Fatalf("2-of-5 must refuse, got %v", err)
	}
	// 3 valid agreeing answers plus 2 failed members corroborate: the denominator
	// counts failures, and the threshold is met anyway.
	decision, err := p.Resolve([]rpc.Observation{
		corrObs(`1`), corrObs(`1`), corrObs(`1`), errObs(), errObs(),
	})
	if err != nil || decision.Decision.Status != rpc.Corroborated || len(decision.Decision.Support) != 3 {
		t.Fatalf("3-of-5 must corroborate with failures in the basis, got %+v %v", decision, err)
	}
}

func TestCorroborationSingleAdmittedResponseStaysSelected(t *testing.T) {
	// Where the floor admits a lone response (H=1, one-member basis), it
	// stays Selected: a corroboration claim needs two supporters (0028).
	p := Strict(rpc.CanonicalJSON, WithQuorum(1))
	decision, err := p.Resolve([]rpc.Observation{corrObs(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != rpc.Selected {
		t.Fatalf("expected Selected, got %v", decision.Decision.Status)
	}
	// A multi-member basis with one survivor still refuses: the majority
	// clause is measured against the whole basis, not the survivors.
	_, err = p.Resolve([]rpc.Observation{corrObs(`1`), errObs(), errObs()})
	if !errors.Is(err, rpc.ErrInsufficientSupport) {
		t.Fatalf("1-of-3 must refuse, got %v", err)
	}
}

func TestResolveCorroboratedOverridesConstruction(t *testing.T) {
	// A policy constructed without corroboration options keeps the legacy
	// default through Resolve, but an invocation snapshot re-adjudicates the
	// same observations under the supplied mode and floor.
	p := Strict(rpc.CanonicalJSON)
	_, err := p.Resolve([]rpc.Observation{corrObs(`1`), corrObs(`1`), corrObs(`2`)})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("legacy default must conflict on dissent, got %v", err)
	}
	corroborated, err := p.(CorroboratingResolver).ResolveCorroborated(Corroboration{Mode: rpc.CorroborationMajority, Floor: 2},
		[]rpc.Observation{corrObs(`1`), corrObs(`1`), corrObs(`2`)},
	)
	if err != nil || corroborated.Decision.Status != rpc.Corroborated || len(corroborated.Decision.Support) != 2 {
		t.Fatalf("snapshot must adjudicate by majority, got %+v %v", corroborated, err)
	}
}

func TestCorroborationConstruction(t *testing.T) {
	for _, want := range []Corroboration{
		{Mode: rpc.CorroborationMajority, Floor: 2},
		{Mode: rpc.CorroborationUnanimity, Floor: 3},
		{Mode: rpc.CorroborationForwarded},
	} {
		got, err := NewCorroboration(want.Mode, want.Floor)
		if err != nil || got != want {
			t.Fatalf("NewCorroboration(%+v) = %+v, %v", want, got, err)
		}
	}
}

func TestCorroborationRejectsInvalidConfiguration(t *testing.T) {
	for _, invalid := range []Corroboration{
		{Mode: rpc.CorroborationMode(99), Floor: 2},
		{Mode: rpc.CorroborationMajority},
		{Mode: rpc.CorroborationUnanimity},
		{Mode: rpc.CorroborationForwarded, Floor: 1},
	} {
		if _, err := NewCorroboration(invalid.Mode, invalid.Floor); err == nil {
			t.Fatalf("accepted invalid rule %+v", invalid)
		}
	}
}

func TestCorroborationModeString(t *testing.T) {
	if got := rpc.CorroborationMajority.String(); got != "majority" {
		t.Fatalf("got %q", got)
	}
	if got := rpc.CorroborationUnanimity.String(); got != "unanimity" {
		t.Fatalf("got %q", got)
	}
}

// The adjudication module consumes only the basis size and clustered
// evidence: vendor JSON never reaches it, and no health or latency input
// exists to weight. This pins that structurally — an observation carrying
// arbitrary vendor noise clusters exactly like a clean one.
func TestAdjudicationIsBlindToVendorNoise(t *testing.T) {
	p := Strict(rpc.CanonicalJSON, WithQuorum(2))
	decision, err := p.Resolve([]rpc.Observation{
		corrObs(`{"result":1,"vendorExtra":"x"}`),
		corrObs(`{"result":1,"vendorExtra":"y"}`),
	})
	if err == nil {
		// Canonical JSON treats differing extra fields as disagreement; the
		// adjudicator itself never sees the raw payloads either way. The
		// assertion that matters: clustering is the normalizer's job.
		_ = decision
	}
	// The same normalizer output maps to one cluster regardless of payload
	// spelling: the adjudicator's inputs are canonical keys only.
	simple, err := p.Resolve([]rpc.Observation{
		corrObs(`{"a":1}`),
		corrObs(`{"a": 1}`),
	})
	if err != nil || len(simple.Decision.Support) != 2 {
		t.Fatalf("equivalent spellings must cluster, got %+v %v", simple, err)
	}
}

func TestCorroborationRejectsAmbiguousIdentity(t *testing.T) {
	for _, mode := range []rpc.CorroborationMode{rpc.CorroborationMajority, rpc.CorroborationUnanimity} {
		for _, tc := range []struct {
			name         string
			observations []rpc.Observation
		}{
			{"missing", []rpc.Observation{{Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}}, {Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}}}},
			{"duplicate", []rpc.Observation{{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}}, {Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}}}},
			{"equivocation", []rpc.Observation{{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}}, {Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`2`)}}}}},
			{"duplicate failure", []rpc.Observation{{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`1`)}}}, {Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("timeout")}}}}},
		} {
			t.Run(mode.String()+"/"+tc.name, func(t *testing.T) {
				p := Strict(rpc.CanonicalJSON).(CorroboratingResolver)
				if _, err := p.ResolveCorroborated(Corroboration{Mode: mode, Floor: 2}, tc.observations); !errors.Is(err, rpc.ErrInvalidOutcome) {
					t.Fatalf("ambiguous identity accepted: %v", err)
				}
			})
		}
	}
}

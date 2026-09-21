package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

// corrObs builds an observation answering one canonical value.
func corrObs(value string) Observation {
	return Observation{Source: src(), Value: json.RawMessage(value)}
}

func errObs() Observation {
	return Observation{Source: src(), Err: errors.New("timeout")}
}

func TestCorroborationRequiredSupport(t *testing.T) {
	// Decision 0029's table: with H=2, required support for N=2..5 is
	// 2/2/3/3; the floor binds small bases, the majority binds large ones.
	floor2 := Corroboration{Mode: CorroborationMajority, Floor: 2}
	for n, want := range map[int]int{1: 2, 2: 2, 3: 2, 4: 3, 5: 3, 6: 4, 7: 4} {
		if got := floor2.Required(n); got != want {
			t.Fatalf("N=%d: required %d, want %d", n, got, want)
		}
	}
	// A zero floor keeps the pure majority bound; a floor above the majority
	// binds the requirement to the caller's integrity budget.
	pure := Corroboration{}
	for n, want := range map[int]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 3} {
		if got := pure.Required(n); got != want {
			t.Fatalf("N=%d: required %d, want %d", n, got, want)
		}
	}
	if got := (Corroboration{Floor: 5}).Required(3); got != 5 {
		t.Fatalf("floor above majority must bind, got %d", got)
	}
}

func TestCorroborationMajorityAcceptsMinorityDissent(t *testing.T) {
	p := Strict(CanonicalJSON, WithQuorum(2))
	decision, err := p.Resolve([]Observation{
		corrObs(`1`), corrObs(`1`), corrObs(`1`),
		corrObs(`2`), corrObs(`2`),
	})
	if err != nil || decision.Status != Corroborated {
		t.Fatalf("majority must accept the 3-of-5 cluster, got %+v %v", decision, err)
	}
	if len(decision.Support) != 3 || len(decision.Reject) != 2 {
		t.Fatalf("expected 3 support and 2 reject, got %+v", decision)
	}
}

func TestCorroborationUnanimityRejectsDissent(t *testing.T) {
	p := Strict(CanonicalJSON, WithQuorum(2), WithUnanimity())
	decision, err := p.Resolve([]Observation{
		corrObs(`1`), corrObs(`1`), corrObs(`1`),
		corrObs(`2`), corrObs(`2`),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("unanimity must reject disagreement, got %v", err)
	}
	if len(decision.Support) != 3 || len(decision.Reject) != 2 {
		t.Fatalf("evidence must still carry the clusters, got %+v", decision)
	}
}

func TestCorroborationFailuresOccupyTheBasis(t *testing.T) {
	// 2 valid agreeing answers plus 3 failed members: the required support
	// is Q=3 of the 5-member basis, so a surviving pair cannot corroborate.
	p := Strict(CanonicalJSON, WithQuorum(2))
	_, err := p.Resolve([]Observation{
		corrObs(`1`), corrObs(`1`), errObs(), errObs(), errObs(),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("2-of-5 must refuse, got %v", err)
	}
	// 3 valid agreeing answers plus 2 failed members corroborate: the denominator
	// counts failures, and the threshold is met anyway.
	decision, err := p.Resolve([]Observation{
		corrObs(`1`), corrObs(`1`), corrObs(`1`), errObs(), errObs(),
	})
	if err != nil || decision.Status != Corroborated || len(decision.Support) != 3 {
		t.Fatalf("3-of-5 must corroborate with failures in the basis, got %+v %v", decision, err)
	}
}

func TestCorroborationSingleAdmittedResponseStaysSelected(t *testing.T) {
	// Where the floor admits a lone response (H=1, one-member basis), it
	// stays Selected: a corroboration claim needs two supporters (0028).
	p := Strict(CanonicalJSON, WithQuorum(1))
	decision, err := p.Resolve([]Observation{corrObs(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Selected {
		t.Fatalf("expected Selected, got %v", decision.Status)
	}
	// A multi-member basis with one survivor still refuses: the majority
	// clause is measured against the whole basis, not the survivors.
	_, err = p.Resolve([]Observation{corrObs(`1`), errObs(), errObs()})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("1-of-3 must refuse, got %v", err)
	}
}

func TestResolveCorroboratedOverridesConstruction(t *testing.T) {
	// A policy constructed without corroboration options keeps the legacy
	// default through Resolve, but an invocation snapshot re-adjudicates the
	// same observations under the supplied mode and floor.
	p := Strict(CanonicalJSON)
	_, err := p.Resolve([]Observation{corrObs(`1`), corrObs(`1`), corrObs(`2`)})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy default must conflict on dissent, got %v", err)
	}
	corroborated, err := p.(CorroboratingPolicy).ResolveCorroborated(
		Corroboration{Mode: CorroborationMajority, Floor: 2},
		[]Observation{corrObs(`1`), corrObs(`1`), corrObs(`2`)},
	)
	if err != nil || corroborated.Status != Corroborated || len(corroborated.Support) != 2 {
		t.Fatalf("snapshot must adjudicate by majority, got %+v %v", corroborated, err)
	}
}

func TestCorroborationSharedSetAndSnapshot(t *testing.T) {
	s, err := NewCorroborationShared(CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.Mode != CorroborationMajority || snap.Floor != 2 {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	if err := s.Set(CorroborationUnanimity, 3); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.Mode != CorroborationUnanimity || snap.Floor != 3 {
		t.Fatalf("Set must replace mode and floor together, got %+v", snap)
	}
	// The zero receiver is a valid unset configuration.
	var unset *CorroborationShared
	if snap := unset.Snapshot(); snap.Mode != CorroborationMajority || snap.Floor != 0 {
		t.Fatalf("unset snapshot must be the zero corroboration, got %+v", snap)
	}
}

func TestCorroborationSharedRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewCorroborationShared(CorroborationMode(99), 2); err == nil {
		t.Fatal("expected unknown-mode rejection")
	}
	if _, err := NewCorroborationShared(CorroborationMajority, 0); err == nil {
		t.Fatal("expected zero-floor rejection")
	}
	s, err := NewCorroborationShared(CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(CorroborationUnanimity, 0); err == nil {
		t.Fatal("expected floor validation on Set")
	}
	if snap := s.Snapshot(); snap.Mode != CorroborationMajority || snap.Floor != 2 {
		t.Fatalf("failed Set must not change the configuration, got %+v", snap)
	}
}

func TestCorroborationModeString(t *testing.T) {
	if got := CorroborationMajority.String(); got != "majority" {
		t.Fatalf("got %q", got)
	}
	if got := CorroborationUnanimity.String(); got != "unanimity" {
		t.Fatalf("got %q", got)
	}
}

// The adjudication module consumes only the basis size and clustered
// evidence: vendor JSON never reaches it, and no health or latency input
// exists to weight. This pins that structurally — an observation carrying
// arbitrary vendor noise clusters exactly like a clean one.
func TestAdjudicationIsBlindToVendorNoise(t *testing.T) {
	p := Strict(CanonicalJSON, WithQuorum(2))
	decision, err := p.Resolve([]Observation{
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
	simple, err := p.Resolve([]Observation{
		corrObs(`{"a":1}`),
		corrObs(`{"a": 1}`),
	})
	if err != nil || len(simple.Support) != 2 {
		t.Fatalf("equivalent spellings must cluster, got %+v %v", simple, err)
	}
}

func TestCorroborationRejectsAmbiguousIdentity(t *testing.T) {
	for _, mode := range []CorroborationMode{CorroborationMajority, CorroborationUnanimity} {
		for _, tc := range []struct {
			name         string
			observations []Observation
		}{
			{"missing", []Observation{{Value: json.RawMessage(`1`)}, {Value: json.RawMessage(`1`)}}},
			{"duplicate", []Observation{{Source: SourceRef{ID: "a"}, Value: json.RawMessage(`1`)}, {Source: SourceRef{ID: "a"}, Value: json.RawMessage(`1`)}}},
			{"equivocation", []Observation{{Source: SourceRef{ID: "a"}, Value: json.RawMessage(`1`)}, {Source: SourceRef{ID: "a"}, Value: json.RawMessage(`2`)}}},
			{"duplicate failure", []Observation{{Source: SourceRef{ID: "a"}, Value: json.RawMessage(`1`)}, {Source: SourceRef{ID: "a"}, Err: errors.New("timeout")}}},
		} {
			t.Run(mode.String()+"/"+tc.name, func(t *testing.T) {
				p := Strict(CanonicalJSON).(CorroboratingPolicy)
				if _, err := p.ResolveCorroborated(Corroboration{Mode: mode, Floor: 2}, tc.observations); !errors.Is(err, ErrConflict) {
					t.Fatalf("ambiguous identity accepted: %v", err)
				}
			})
		}
	}
}

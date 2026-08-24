package rpc

import (
	"encoding/json"
	"errors"
	"testing"

	"ethguard/pool"
)

// src returns a distinct opaque source pointer so Support/Reject membership
// can be counted without constructing real connections.
func src() *pool.Source { return &pool.Source{} }

func obs(value string) Observation {
	return Observation{Source: src(), Value: json.RawMessage(value)}
}

func TestStrictUnanimousVerifiesWithCorroboration(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		obs(`{"a":1}`),
		obs(`{"a":1}`),
		obs(`{"a": 1}`), // key order and spacing must not matter
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Verified {
		t.Fatalf("expected verified, got %v", decision.Status)
	}
	if len(decision.Support) != 3 {
		t.Fatalf("expected three supporters, got %+v", decision)
	}
}

func TestStrictSingleResponseDowngradesToSelected(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		{Source: src(), Err: errors.New("timeout")},
		obs(`true`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Selected {
		t.Fatalf("expected selected for uncorroborated response, got %v", decision.Status)
	}
}

func TestStrictDissentConflictsByDefault(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		obs(`1`), obs(`1`), obs(`1`),
		obs(`2`),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if len(decision.Support) != 3 || len(decision.Reject) != 1 {
		t.Fatalf("expected 3 support and 1 reject, got %+v", decision)
	}
	if string(decision.Value) != `1` {
		t.Fatalf("expected majority value to win evidence, got %s", decision.Value)
	}
}

func TestStrictTieBreakIsDeterministic(t *testing.T) {
	p := Strict(CanonicalJSON)
	for range 32 {
		decision, err := p.Resolve([]Observation{
			obs(`2`), obs(`2`),
			obs(`1`), obs(`1`),
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("expected conflict on tie, got %v", err)
		}
		if string(decision.Value) != `1` {
			t.Fatalf("tie winner drifted: %s", decision.Value)
		}
	}
}

func TestQuorumRequiresFloorAndFleetMajority(t *testing.T) {
	fleet := func(values []string, failures int) []Observation {
		observers := make([]Observation, 0, len(values)+failures)
		for _, v := range values {
			observers = append(observers, obs(v))
		}
		for range failures {
			observers = append(observers, Observation{Source: src(), Err: errors.New("timeout")})
		}
		return observers
	}

	cases := []struct {
		name     string
		quorum   int
		values   []string
		failures int
		wantErr  bool
	}{
		{"floor and majority met", 3, []string{`1`, `1`, `1`, `2`}, 1, false}, // 3/5 attempted
		{"floor unmet despite unanimity among survivors", 3, []string{`1`, `1`}, 3, true},
		{"majority of fleet unmet", 2, []string{`1`, `1`}, 3, true}, // unanimous survivors but 2/5
		{"floor above top cluster", 4, []string{`1`, `1`, `1`, `2`, `2`}, 0, true},
		{"floor met with full fleet", 5, []string{`1`, `1`, `1`, `1`, `1`}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Strict(CanonicalJSON, WithQuorum(tc.quorum))
			decision, err := p.Resolve(fleet(tc.values, tc.failures))
			if tc.wantErr {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("expected conflict, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != Verified {
				t.Fatalf("expected verified, got %v", decision.Status)
			}
		})
	}
}

func TestWithQuorumPanicsOnNonPositive(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on non-positive quorum")
		}
	}()
	Strict(CanonicalJSON, WithQuorum(0))
}

func TestNormalizeFailureBecomesFault(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		obs(`1`),
		obs(`1`),
		{Source: src(), Value: json.RawMessage(`not-json`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("expected one normalize fault, got %+v", decision.Faults)
	}
	if decision.Status != Verified || len(decision.Support) != 2 {
		t.Fatalf("valid responses should still verify, got %+v", decision)
	}
}

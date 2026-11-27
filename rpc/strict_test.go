package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/codermaybe/ethguard/rpctest"
)

// src returns a distinct source identity without constructing real connections.
var sourceCounter atomic.Uint64

func src() SourceRef {
	n := sourceCounter.Add(1)
	return SourceRef{Name: fmt.Sprintf("source-%d", n), ID: fmt.Sprintf("id-%d", n)}
}

func obs(value string) Observation {
	return Observation{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(value)}}}
}

func TestStrictUnanimousCorroborates(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		obs(`{"a":1}`),
		obs(`{"a":1}`),
		obs(`{"a": 1}`), // key order and spacing must not matter
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Corroborated {
		t.Fatalf("expected corroborated, got %v", decision.Decision.Status)
	}
	if len(decision.Decision.Support) != 3 {
		t.Fatalf("expected three supporters, got %+v", decision)
	}
}

func TestStrictSingleResponseDowngradesToSelected(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
		obs(`true`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Selected {
		t.Fatalf("expected selected for uncorroborated response, got %v", decision.Decision.Status)
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
	if len(decision.Decision.Support) != 3 || len(decision.Decision.Reject) != 1 {
		t.Fatalf("expected 3 support and 1 reject, got %+v", decision)
	}
	if string(decision.Decision.Value) != `1` {
		t.Fatalf("expected majority value to win evidence, got %s", decision.Decision.Value)
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
		if string(decision.Decision.Value) != `1` {
			t.Fatalf("tie winner drifted: %s", decision.Decision.Value)
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
			observers = append(observers, Observation{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}})
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
				want := ErrInsufficientSupport
				if tc.name == "floor above top cluster" {
					want = ErrConflict
				}
				if !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if decision.Decision.Status != Corroborated {
				t.Fatalf("expected corroborated, got %v", decision.Decision.Status)
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

func TestQuorumSingleSourceFleetStaysSelected(t *testing.T) {
	// Floor and majority both admit a lone response under quorum 1, but
	// WithQuorum relaxes the agreement clause only: one uncorroborated
	// response stays Selected, never Corroborated.
	p := Strict(CanonicalJSON, WithQuorum(1))
	decision, err := p.Resolve([]Observation{obs(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Selected {
		t.Fatalf("expected selected for a single uncorroborated response, got %v", decision.Decision.Status)
	}
}

func TestQuorumSingleSurvivorConflicts(t *testing.T) {
	// A multi-source fleet with one survivor fails the fleet majority even
	// under quorum 1: the participation defense is not bypassed by the
	// downgrade path.
	p := Strict(CanonicalJSON, WithQuorum(1))
	_, err := p.Resolve([]Observation{
		obs(`1`),
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
	})
	if !errors.Is(err, ErrInsufficientSupport) {
		t.Fatalf("expected conflict for a lone survivor of three, got %v", err)
	}
}

func TestQuorumTwoSupportersCorroborate(t *testing.T) {
	// Corroboration, floor, and fleet majority together corroborate as before.
	p := Strict(CanonicalJSON, WithQuorum(1))
	decision, err := p.Resolve([]Observation{obs(`1`), obs(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Corroborated {
		t.Fatalf("expected corroborated with two supporters, got %v", decision.Decision.Status)
	}
}

// TestStrictProvenanceStaysTheFirstSupporter'sOwnEvidence pins the
// representative rule at the cluster seam: the winner's evidence is the first
// supporter's own, and a later supporter's evidence is never substituted
// when the first supporter has none.
func TestStrictProvenanceStaysTheFirstSupporterOwnEvidence(t *testing.T) {
	p := Strict(CanonicalJSON)
	first, later := src(), src()
	decision, err := p.Resolve([]Observation{
		{Attempted: true, Source: first, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`1`)}}},
		{Attempted: true, Source: later, Evidence: json.RawMessage(`"later-evidence"`), Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(` 1 `)}}},
	})
	if err != nil || decision.Decision.Status != Corroborated {
		t.Fatalf("decision = %+v, %v, want a corroborated pair", decision.Decision.Status, err)
	}
	if decision.Decision.Representative != first {
		t.Fatalf("representative = %+v, want the first supporter", decision.Decision.Representative)
	}
	if len(decision.Decision.Provenance) != 0 {
		t.Fatalf("provenance = %s, want empty: a later supporter's evidence is never substituted", decision.Decision.Provenance)
	}
}

func TestNormalizeFailureBecomesFault(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		obs(`1`),
		obs(`1`),
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`not-json`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Decision.Faults) != 1 {
		t.Fatalf("expected one normalize fault, got %+v", decision.Decision.Faults)
	}
	if decision.Decision.Status != Corroborated || len(decision.Decision.Support) != 2 {
		t.Fatalf("valid responses should still corroborate, got %+v", decision)
	}
}

func TestStrictClassifiesFleetWithoutValidResponses(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		obs(`not-json`),
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
	})
	if !errors.Is(err, ErrNoValidResponse) {
		t.Fatalf("expected no-valid-response class, got %v", err)
	}
	if len(decision.Decision.Faults) != 2 {
		t.Fatalf("expected the normalize failure on faults, got %+v", decision.Decision.Faults)
	}
}

func TestForwardWithoutResponseIsNoValidResponse(t *testing.T) {
	p := ForwardPolicy{}
	if _, err := p.Resolve([]Observation{{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("down")}}}}); !errors.Is(err, ErrNoValidResponse) {
		t.Fatalf("expected no-valid-response class, got %v", err)
	}
}

func TestStrictCanOptIntoRPCErrorBodyClustering(t *testing.T) {
	p := Strict(CanonicalJSON, WithRPCErrorBodies())
	decision, err := p.Resolve([]Observation{
		{Attempted: true, Source: src(), Response: Response{
			Exchange: Exchange{Progress: ExchangeCompleted},
			Answer:   Answer{Kind: AnswerProtocolError, RPCError: &RPCError{Code: -32000, Message: "execution reverted"}, Cause: rpctest.RPCError(-32000, "execution reverted")},
		},
		},
		{Attempted: true, Source: src(), Response: Response{
			Exchange: Exchange{Progress: ExchangeCompleted},
			Answer:   Answer{Kind: AnswerProtocolError, RPCError: &RPCError{Code: -32000, Message: "execution reverted"}, Cause: rpctest.RPCError(-32000, "execution reverted")},
		},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Corroborated || len(decision.Decision.Support) != 2 {
		t.Fatalf("expected clustered protocol errors, got %+v", decision)
	}
}

func TestFinishRejectsCanonicalDisagreement(t *testing.T) {
	f := strictFunc{encode: func(raw json.RawMessage) (json.RawMessage, error) {
		return append(json.RawMessage(nil), raw...), nil
	}}
	decision := Decision{
		Status:         Corroborated,
		Value:          json.RawMessage(`"0xA"`),
		Representative: SourceRef{Name: "a", ID: "a"},
	}
	winner := cluster{canonical: []json.RawMessage{
		json.RawMessage(`"0x1"`),
		json.RawMessage(`"0x01"`),
	}}
	got, err := f.finish(decision, nil, []cluster{winner})
	if !errors.Is(err, ErrInvalidCanonicalResult) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("err = %v", err)
	}
	if got.Status != Unknown || len(got.Value) != 0 {
		t.Fatalf("decision = %+v", got)
	}
	if got.Representative != decision.Representative {
		t.Fatalf("representative changed to %+v", got.Representative)
	}
}

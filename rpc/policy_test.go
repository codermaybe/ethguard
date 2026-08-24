package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestStrictReturnsConflictEvidence(t *testing.T) {
	p := Strict(CanonicalJSON)
	decision, err := p.Resolve([]Observation{
		{Value: json.RawMessage(`1`)},
		{Value: json.RawMessage(`2`)},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if len(decision.Support) != 1 || len(decision.Reject) != 1 {
		t.Fatalf("expected one support and one reject, got %+v", decision)
	}

	// Conflict evidence must be reproducible regardless of map iteration
	// order: the smallest canonical key always wins.
	for range 32 {
		again, err := p.Resolve([]Observation{
			{Value: json.RawMessage(`2`)},
			{Value: json.RawMessage(`1`)},
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("expected conflict, got %v", err)
		}
		if string(again.Value) != string(decision.Value) {
			t.Fatalf("conflict winner drifted: %s vs %s", again.Value, decision.Value)
		}
	}
}

func TestRegistryUsesForwardPolicyForUnknownMethods(t *testing.T) {
	policy := Registry{}.PolicyFor("eth_unknown")
	decision, err := policy.Resolve([]Observation{{Value: json.RawMessage(`true`)}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Forwarded {
		t.Fatalf("expected forwarded status, got %v", decision.Status)
	}
	if _, err := policy.Prepare(context.Background(), Request{Method: "eth_unknown"}); err != nil {
		t.Fatal(err)
	}
}

func TestForwardMarksDecisionAsForwarded(t *testing.T) {
	p := ForwardPolicy{}
	decision, err := p.Resolve([]Observation{{Value: json.RawMessage(`true`)}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Forwarded {
		t.Fatalf("expected forwarded status, got %v", decision.Status)
	}
}

func TestSelectedMarksDecisionAsSelected(t *testing.T) {
	p := SelectedPolicy{}
	decision, err := p.Resolve([]Observation{{Value: json.RawMessage(`true`)}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Selected {
		t.Fatalf("expected selected status, got %v", decision.Status)
	}
}

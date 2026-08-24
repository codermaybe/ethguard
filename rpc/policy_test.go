package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestStrictPolicyReturnsConflictEvidence(t *testing.T) {
	p := StrictPolicy{}
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

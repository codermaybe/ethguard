package rpc

import (
	"context"
	"encoding/json"
	"testing"
)

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

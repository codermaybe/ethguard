package rpc

import (
	"encoding/json"
	"testing"
)

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

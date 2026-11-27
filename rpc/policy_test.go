package rpc

import (
	"encoding/json"
	"testing"
)

func TestForwardMarksDecisionAsForwarded(t *testing.T) {
	p := ForwardPolicy{}
	decision, err := p.Resolve([]Observation{{Source: SourceRef{ID: "a"}, Attempted: true, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`true`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Forwarded {
		t.Fatalf("expected forwarded status, got %v", decision.Decision.Status)
	}
}

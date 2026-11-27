package rpcengine

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestForwardMarksDecisionAsForwarded(t *testing.T) {
	p := ForwardResolver{}
	decision, err := p.Resolve([]rpc.Observation{{Source: rpc.SourceRef{ID: "a"}, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`true`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != rpc.Forwarded {
		t.Fatalf("expected forwarded status, got %v", decision.Decision.Status)
	}
}

package methods

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestBlockNumberAndBlobBaseFeeLowerMedian(t *testing.T) {
	sources := []rpc.SourceRef{
		{ID: "s1", Name: "source-1"},
		{ID: "s2", Name: "source-2"},
		{ID: "s3", Name: "source-3"},
	}

	t.Run("blockNumber median", func(t *testing.T) {
		policy := compiledPlan(t, MethodBlockNumber).Resolver
		observations := []rpc.Observation{
			{Attempted: true, Source: sources[0], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x10"`)}}}, // 16
			{Attempted: true, Source: sources[1], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x20"`)}}}, // 32
			{Attempted: true, Source: sources[2], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x18"`)}}}, // 24
		}
		decision, err := policy.Resolve(observations)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Decision.Status != rpc.Selected {
			t.Fatalf("status = %v, want Selected", decision.Decision.Status)
		}
		// Lower median of {16, 24, 32} is 24 -> "0x18"
		if string(decision.Decision.Value) != `"0x18"` {
			t.Fatalf("value = %s, want %s", decision.Decision.Value, `"0x18"`)
		}
	})

	t.Run("blobBaseFee median", func(t *testing.T) {
		policy := compiledPlan(t, MethodBlobBaseFee).Resolver
		observations := []rpc.Observation{
			{Attempted: true, Source: sources[0], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x1"`)}}},
			{Attempted: true, Source: sources[1], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x3"`)}}},
			{Attempted: true, Source: sources[2], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x2"`)}}},
		}
		decision, err := policy.Resolve(observations)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Decision.Status != rpc.Selected {
			t.Fatalf("status = %v, want Selected", decision.Decision.Status)
		}
		// Lower median of {1, 2, 3} is 2 -> "0x2"
		if string(decision.Decision.Value) != `"0x2"` {
			t.Fatalf("value = %s, want %s", decision.Decision.Value, `"0x2"`)
		}
	})
}

func TestCreateAccessListPolicyAnchoringAndNormalization(t *testing.T) {
	plan := compiledPlan(t, MethodCreateAccessList)
	const anchored = "0x1122334455667788990011223344556677889900112233445566778899001122"

	t.Run("prepares latest tag to anchor hash", func(t *testing.T) {
		req := rpc.Request{
			Method: MethodCreateAccessList,
			Params: []any{map[string]any{"to": "0x1234"}, "latest"},
		}
		ref := rpc.ReferenceEvidence{Hash: anchored, Status: rpc.Corroborated}
		prepared, pin, err := plan.PrepareWithReference(req, ref, true)
		if err != nil {
			t.Fatal(err)
		}
		if pin != anchored {
			t.Fatalf("pin = %q, want the established hash", pin)
		}
		if len(prepared.Params) != 2 || prepared.Params[1] != anchored {
			t.Fatalf("prepared params = %+v, want anchored hash", prepared.Params)
		}
	})

	t.Run("resolves matching responses with different key order or hex casing", func(t *testing.T) {
		sources := []rpc.SourceRef{
			{ID: "s1", Name: "source-1"},
			{ID: "s2", Name: "source-2"},
		}
		resp1 := json.RawMessage(`{"accessList":[{"address":"0x0000000000000000000000000000000000000ABC","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001"]}],"gasUsed":"0x5208"}`)
		resp2 := json.RawMessage(`{"gasUsed":"0x5208","accessList":[{"address":"0x0000000000000000000000000000000000000abc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001"]}]}`)

		observations := []rpc.Observation{
			{Attempted: true, Source: sources[0], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: resp1}}},
			{Attempted: true, Source: sources[1], Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: resp2}}},
		}
		decision, err := plan.Resolver.Resolve(observations)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Decision.Status != rpc.Corroborated {
			t.Fatalf("status = %v, want Corroborated", decision.Decision.Status)
		}
		if len(decision.Decision.Support) != 2 {
			t.Fatalf("support = %d, want 2", len(decision.Decision.Support))
		}
	})
}

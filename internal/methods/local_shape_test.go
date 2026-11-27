package methods

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// TestLocalWholeAnswerShapeAdmission pins 0061's top-level shape gate for
// upstream-local methods: absence is not a negative state and malformed
// shapes do not become Selected values.
func TestLocalWholeAnswerShapeAdmission(t *testing.T) {
	cases := []struct {
		name     string
		policy   rpcengine.Resolver
		accepted []string
		rejected []string
	}{
		{
			name:     "eth_syncing",
			policy:   compiledPlan(t, MethodSyncing).Resolver,
			accepted: []string{`false`, `{"startingBlock":"0x1","currentBlock":"0x2","highestBlock":"0x3"}`},
			rejected: []string{`null`, `true`, `[]`, `"false"`, `0`, `{} trailing`},
		},
		{
			name:     "net_listening",
			policy:   compiledPlan(t, MethodListening).Resolver,
			accepted: []string{`true`, `false`, ` true `},
			rejected: []string{`null`, `"true"`, `1`, `[]`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, raw := range tc.accepted {
				decision, err := tc.policy.Resolve([]rpc.Observation{{Attempted: true, Source: rpc.SourceRef{Name: "s0", ID: "s0"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(raw)}}}})
				if err != nil || decision.Decision.Status != rpc.Selected || len(decision.Decision.Value) == 0 {
					t.Fatalf("accepted %s: status %v value %s err %v", raw, decision.Decision.Status, decision.Decision.Value, err)
				}
			}
			for _, raw := range tc.rejected {
				decision, err := tc.policy.Resolve([]rpc.Observation{{Attempted: true, Source: rpc.SourceRef{Name: "s0", ID: "s0"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(raw)}}}})
				if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Decision.Status != rpc.Unknown || len(decision.Decision.Value) != 0 {
					t.Fatalf("rejected %s: status %v value %s err %v", raw, decision.Decision.Status, decision.Decision.Value, err)
				}
			}
		})
	}
}

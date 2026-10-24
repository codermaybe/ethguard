package methods

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// TestLocalWholeAnswerShapeAdmission pins 0061's top-level shape gate for
// upstream-local methods: absence is not a negative state and malformed
// shapes do not become Selected values.
func TestLocalWholeAnswerShapeAdmission(t *testing.T) {
	cases := []struct {
		name     string
		policy   rpc.Policy
		accepted []string
		rejected []string
	}{
		{
			name:     "eth_syncing",
			policy:   SyncingPolicy(),
			accepted: []string{`false`, `{"startingBlock":"0x1","currentBlock":"0x2","highestBlock":"0x3"}`},
			rejected: []string{`null`, `true`, `[]`, `"false"`, `0`, `{} trailing`},
		},
		{
			name:     "net_listening",
			policy:   ListeningPolicy(),
			accepted: []string{`true`, `false`, ` true `},
			rejected: []string{`null`, `"true"`, `1`, `[]`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, raw := range tc.accepted {
				decision, err := tc.policy.Resolve([]rpc.Observation{{Source: rpc.SourceRef{Name: "s0", ID: "s0"}, Value: json.RawMessage(raw)}})
				if err != nil || decision.Status != rpc.Selected || len(decision.Value) == 0 {
					t.Fatalf("accepted %s: status %v value %s err %v", raw, decision.Status, decision.Value, err)
				}
			}
			for _, raw := range tc.rejected {
				decision, err := tc.policy.Resolve([]rpc.Observation{{Source: rpc.SourceRef{Name: "s0", ID: "s0"}, Value: json.RawMessage(raw)}})
				if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
					t.Fatalf("rejected %s: status %v value %s err %v", raw, decision.Status, decision.Value, err)
				}
			}
		})
	}
}

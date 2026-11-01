package ethguard_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
)

func TestMixedSourcesPreserveCallErrorData(t *testing.T) {
	for _, tc := range []struct {
		name        string
		httpData    string
		wsData      string
		presence    [2]rpc.ErrorDataPresenceState
		sameKey     bool
		wantValue   string
		abstain     int
		dataSupport int
		returned    bool
	}{
		{"missing and null abstain separately", "", `,"data":null`, [2]rpc.ErrorDataPresenceState{rpc.ErrorDataPresenceMissing, rpc.ErrorDataPresenceNull}, false, `{}`, 2, 0, false},
		{"empty bytes agree", `,"data":"0x"`, `,"data":"0x"`, [2]rpc.ErrorDataPresenceState{rpc.ErrorDataPresenceValue, rpc.ErrorDataPresenceValue}, true, `{"data":"0x"}`, 0, 2, true},
		{"concrete bytes canonicalize", `,"data":"0xDEAD"`, `,"data":"0xdead"`, [2]rpc.ErrorDataPresenceState{rpc.ErrorDataPresenceValue, rpc.ErrorDataPresenceValue}, true, `{"data":"0xdead"}`, 0, 2, true},
		{"null cannot support concrete bytes", `,"data":null`, `,"data":"0xdead"`, [2]rpc.ErrorDataPresenceState{rpc.ErrorDataPresenceNull, rpc.ErrorDataPresenceValue}, false, `{}`, 1, 1, false},
		{"empty and value disagree", `,"data":"0x"`, `,"data":"0xdead"`, [2]rpc.ErrorDataPresenceState{rpc.ErrorDataPresenceValue, rpc.ErrorDataPresenceValue}, false, `{}`, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peers := []*sourceWirePeer{
				newSourceWirePeer(t, false, func(req sourceWireRequest) string {
					return `"error":{"code":3,"message":"execution reverted"` + tc.httpData + `}`
				}),
				newSourceWirePeer(t, true, func(req sourceWireRequest) string {
					return `"error":{"code":3,"message":"execution reverted"` + tc.wsData + `}`
				}),
			}
			client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
				{ID: "http", Endpoint: peers[0].endpoint},
				{ID: "wss", Endpoint: peers[1].endpoint, Transport: config.TransportWSS},
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			params := []any{map[string]any{"to": "0x0000000000000000000000000000000000000001", "data": "0x"}, "0x10"}
			emissions, err := client.Pool().Broadcast(ctx, "eth_call", params)
			if err != nil || len(emissions) != 2 {
				t.Fatalf("raw call: %+v %v", emissions, err)
			}
			var keys [2]string
			for i, emission := range emissions {
				body := aggregator.ObservationRPCError(emission.Err)
				if body == nil || body.DataPresence != tc.presence[i] {
					t.Fatalf("source %d lost wire presence: body=%+v err=%v", i, body, emission.Err)
				}
				keys[i], err = client.Normalize("eth_call", body.Raw())
				if err != nil {
					t.Fatal(err)
				}
			}
			if (keys[0] == keys[1]) != tc.sameKey {
				t.Fatalf("wire presence/data comparison collapsed or split incorrectly: %q", keys)
			}
			decision, err := client.Call(ctx, "eth_call", params...)
			if err != nil || decision.Status != rpc.Corroborated || string(decision.Value) != tc.wantValue || len(decision.Participants) != 2 || len(decision.Support) != 2 {
				t.Fatalf("call=%+v value=%s err=%v", decision, decision.Value, err)
			}
			if len(decision.Dimensions) != 2 {
				t.Fatalf("dimensions=%+v", decision.Dimensions)
			}
			data := decision.Dimensions[1]
			if data.Name != "data" || data.Returned != tc.returned || len(data.Abstain) != tc.abstain || len(data.Support) != tc.dataSupport {
				t.Fatalf("data dimension=%+v", data)
			}
			if !tc.returned {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(decision.Value, &fields); err != nil {
					t.Fatal(err)
				}
				if _, ok := fields["data"]; ok {
					t.Fatalf("unconfirmed data exposed: %s", decision.Value)
				}
			}
			for i, source := range client.Pool().VotingMembers() {
				if h := source.Health(); h.Failures != 0 || h.Successes != 2 {
					t.Fatalf("source %d revert health=%+v", i, h)
				}
			}
		})
	}
}

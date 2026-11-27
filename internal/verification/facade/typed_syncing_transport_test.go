package ethguard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum"
)

// These synthetic wire fixtures exercise the public projection, not provider
// behavior. Each remaining counter must independently keep upstream Done false
// even when the block heights match.
func TestSyncProgressTransportProjectionAndDone(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want *ethereum.SyncProgress
		done bool
	}{
		{
			name: "distinct fields",
			raw:  `{"startingBlock":"0x1","currentBlock":"0x20","highestBlock":"0x20","syncedAccessLists":"0x11","totalAccessLists":"0x12","trieGenProgress":"0x13","txIndexFinishedBlocks":"0x14","txIndexRemainingBlocks":"0x15","stateIndexRemaining":"0x16","trienodeIndexRemaining":"0x100000017"}`,
			want: &ethereum.SyncProgress{
				StartingBlock:          1,
				CurrentBlock:           32,
				HighestBlock:           32,
				SyncedAccessLists:      17,
				TotalAccessLists:       18,
				TrieGenProgress:        19,
				TxIndexFinishedBlocks:  20,
				TxIndexRemainingBlocks: 21,
				StateIndexRemaining:    22,
				TrienodeIndexRemaining: 0x100000017,
			},
		},
		{
			name: "transaction indexing remaining",
			raw:  `{"startingBlock":"0x1","currentBlock":"0x20","highestBlock":"0x20","txIndexRemainingBlocks":"0x15"}`,
			want: &ethereum.SyncProgress{StartingBlock: 1, CurrentBlock: 32, HighestBlock: 32, TxIndexRemainingBlocks: 21},
		},
		{
			name: "state indexing remaining",
			raw:  `{"startingBlock":"0x1","currentBlock":"0x20","highestBlock":"0x20","stateIndexRemaining":"0x16"}`,
			want: &ethereum.SyncProgress{StartingBlock: 1, CurrentBlock: 32, HighestBlock: 32, StateIndexRemaining: 22},
		},
		{
			name: "trienode indexing remaining",
			raw:  `{"startingBlock":"0x1","currentBlock":"0x20","highestBlock":"0x20","trienodeIndexRemaining":"0x100000017"}`,
			want: &ethereum.SyncProgress{StartingBlock: 1, CurrentBlock: 32, HighestBlock: 32, TrienodeIndexRemaining: 0x100000017},
		},
		{
			name: "complete",
			raw:  `{"startingBlock":"0x1","currentBlock":"0x20","highestBlock":"0x20","syncedAccessLists":"0x12","totalAccessLists":"0x12","trieGenProgress":"0x64","txIndexFinishedBlocks":"0x20","txIndexRemainingBlocks":"0x0","stateIndexRemaining":"0x0","trienodeIndexRemaining":"0x0"}`,
			want: &ethereum.SyncProgress{
				StartingBlock:         1,
				CurrentBlock:          32,
				HighestBlock:          32,
				SyncedAccessLists:     18,
				TotalAccessLists:      18,
				TrieGenProgress:       100,
				TxIndexFinishedBlocks: 32,
			},
			done: true,
		},
		{
			name: "optional fields absent",
			raw:  `{"startingBlock":"0x1","currentBlock":"0x20","highestBlock":"0x20"}`,
			want: &ethereum.SyncProgress{StartingBlock: 1, CurrentBlock: 32, HighestBlock: 32},
			done: true,
		},
		{name: "not syncing", raw: `false`},
	}
	for _, transport := range []struct {
		name string
		ws   bool
	}{
		{name: "http"},
		{name: "wss", ws: true},
	} {
		t.Run(transport.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					answer := func(req sourceWireRequest) string {
						if req.Method == methods.MethodSyncing {
							return `"result":` + tc.raw
						}
						return `"result":"0x1"`
					}
					var upstreams []config.Upstream
					for _, id := range []string{"a", "b"} {
						peer := newSourceWirePeer(t, transport.ws, answer)
						upstream := config.Upstream{ID: id, Endpoint: peer.endpoint}
						if transport.ws {
							upstream.Transport = config.TransportWSS
						}
						upstreams = append(upstreams, upstream)
					}
					client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: upstreams})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(client.Close)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					for _, view := range []string{"Root", "EthClient"} {
						t.Run(view, func(t *testing.T) {
							var progress *ethapi.SyncProgress
							base, evidenceErr := ethguard.WithSourceEvidence(ctx, 1<<20)
							if evidenceErr != nil {
								t.Fatal(evidenceErr)
							}
							capturedCtx, capture := ethguard.WithResult(base)
							var err error
							if view == "Root" {
								var result ethguard.CallResult
								progress, result, err = client.SyncProgress(capturedCtx)
								if err == nil {
									if result.Decision == nil || result.Decision.Status != rpc.Selected {
										t.Fatalf("decision = %+v, want selected", result.Decision)
									}
								}
							} else {
								progress, err = client.EthClient().SyncProgress(capturedCtx)
							}
							if err != nil {
								t.Fatal(err)
							}
							var want *ethapi.SyncProgress
							if tc.want != nil {
								starting := ethapi.Quantity(fmt.Sprintf("0x%x", tc.want.StartingBlock))
								current := ethapi.Quantity(fmt.Sprintf("0x%x", tc.want.CurrentBlock))
								highest := ethapi.Quantity(fmt.Sprintf("0x%x", tc.want.HighestBlock))
								want = &ethapi.SyncProgress{StartingBlock: &starting, CurrentBlock: &current, HighestBlock: &highest}
							}
							if !reflect.DeepEqual(progress, want) {
								t.Fatalf("official core = %+v, want %+v", progress, want)
							}
							result, published, captureErr := capture.Result()
							if !published || captureErr != nil || result.Decision == nil || result.Decision.Status != rpc.Selected || result.Uncertain == nil || len(result.Uncertain.Raw) == 0 {
								t.Fatalf("source material missing: %+v, %v", result, captureErr)
							}
							if tc.want != nil {
								// Compare the exact provider keys independently of the generated model.
								var raw map[string]json.RawMessage
								if err := json.Unmarshal(result.Uncertain.Raw[0].Value, &raw); err != nil {
									t.Fatal(err)
								}
								var expectedRaw map[string]json.RawMessage
								if err := json.Unmarshal([]byte(tc.raw), &expectedRaw); err != nil {
									t.Fatal(err)
								}
								if !reflect.DeepEqual(raw, expectedRaw) {
									t.Fatalf("source counters changed: %s", result.Uncertain.Raw[0].Value)
								}
								if done := syncSourceReady(t, raw); done != tc.done {
									t.Fatalf("preserved provider readiness = %v, want %v", done, tc.done)
								}
							}

						})
					}
				})
			}
		})
	}
}

// Readiness uses provider-specific counters from retained source material, not
// additional fields smuggled into the official generated progress object.
func syncSourceReady(t *testing.T, fields map[string]json.RawMessage) bool {
	t.Helper()
	quantity := func(key string) *big.Int {
		raw, present := fields[key]
		if !present {
			return new(big.Int)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		n, ok := new(big.Int).SetString(value, 0)
		if !ok {
			t.Fatalf("invalid source quantity %s=%s", key, raw)
		}
		return n
	}
	if quantity("currentBlock").Cmp(quantity("highestBlock")) < 0 {
		return false
	}
	for _, key := range []string{"txIndexRemainingBlocks", "stateIndexRemaining", "trienodeIndexRemaining"} {
		if quantity(key).Sign() != 0 {
			return false
		}
	}
	return true
}

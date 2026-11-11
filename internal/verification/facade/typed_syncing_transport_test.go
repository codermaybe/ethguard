package ethguard_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
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
							var progress *ethereum.SyncProgress
							var err error
							if view == "Root" {
								var result ethguard.CallResult
								progress, result, err = client.SyncProgress(ctx)
								if err == nil {
									if result.Decision == nil || result.Decision.Status != rpc.Selected {
										t.Fatalf("decision = %+v, want selected", result.Decision)
									}
								}
							} else {
								progress, err = client.EthClient().SyncProgress(ctx)
							}
							if err != nil || !reflect.DeepEqual(progress, tc.want) {
								t.Fatalf("SyncProgress = %+v, err=%v, want %+v", progress, err, tc.want)
							}
							if progress != nil && progress.Done() != tc.done {
								t.Fatalf("Done = %v, want %v for %+v", progress.Done(), tc.done, progress)
							}
						})
					}
				})
			}
		})
	}
}

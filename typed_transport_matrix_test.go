package ethguard_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// TestTypedTransportMatrix exercises the same typed reads through native HTTP
// and the raw WSS adapter. sourceWirePeer is deliberately shared with the
// mixed-source tests, so this closes the transport-to-execution seam without
// adding a second server or a subscription fixture.
func TestTypedTransportMatrix(t *testing.T) {
	rawBlock := fixtureBlock(t, 17)
	peers := []*sourceWirePeer{
		newSourceWirePeer(t, false, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			return `"result":"0x1"`
		}),
		newSourceWirePeer(t, true, func(req sourceWireRequest) string {
			if req.Method == methods.MethodGetBlockByNumber {
				return `"result":` + string(rawBlock)
			}
			return `"result":"0x1"`
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

	id, _, err := client.ChainID(ctx)
	if err != nil || id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ChainID = %v, err=%v", id, err)
	}
	header, _, err := client.HeaderByNumber(ctx, big.NewInt(17))
	if err != nil || header == nil || header.Number.Uint64() != 17 {
		t.Fatalf("HeaderByNumber = %v, err=%v", header, err)
	}
	if peers[0].connections.Load() == 0 || peers[1].connections.Load() == 0 {
		t.Fatalf("transport connections = http:%d wss:%d", peers[0].connections.Load(), peers[1].connections.Load())
	}
	// Both transports must have received the identity probe and the typed
	// header request. The WSS peer uses the same JSON-RPC request bytes as HTTP;
	// its frame lifecycle is covered by internal/transport/wss contract tests.
	for i, peer := range peers {
		if peer.requests.Load() < 2 {
			t.Fatalf("peer %d request count = %d, want identity + header", i, peer.requests.Load())
		}
	}
}

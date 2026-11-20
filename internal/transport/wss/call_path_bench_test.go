package wss

import (
	"context"
	"encoding/json"
	"testing"
)

// BenchmarkCallRoundTrip measures one unary JSON-RPC exchange over the raw
// WSS adapter on a loopback socket: encode, single-writer frame, server
// echo, read-loop demux, and terminal rpcwire delivery. It is the
// end-to-end reference for any ownership change on the read loop's frame
// handover — the per-message buffer is freshly assembled by the frame codec
// and delivered to exactly one waiter, so a copy at handover can only be
// justified by a measured end-to-end win, never assumed.
func BenchmarkCallRoundTrip(b *testing.B) {
	peerURL := newScriptedPeer(b, func(peer *scriptedPeer) {
		for request := range peer.requests {
			peer.respond(request, `"0x1b4"`)
		}
	})
	tr := dialTestTransport(b, peerURL, 0)
	ctx := context.Background()
	payload := []any{"0x1", "latest"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var raw json.RawMessage
		if err := tr.CallContext(ctx, &raw, "eth_getBalance", payload...); err != nil {
			b.Fatal(err)
		}
	}
}

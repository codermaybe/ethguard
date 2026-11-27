package http

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpctest"
)

// benchParams builds one frozen element list of roughly size bytes, the
// shape of a signed raw transaction parameter that every broadcast member
// re-encodes from the same frozen elements.
func benchParams(size int) []any {
	rawTx := strings.Repeat("ab", max(0, size/2-8))
	return []any{json.RawMessage(`"0x02f8` + rawTx + `"`)}
}

// BenchmarkExchangeEndToEnd measures one full native-HTTP exchange against
// an in-process JSON-RPC node: request encoding, loopback POST, response
// read, and the shared rpcwire terminal delivery. It is the denominator the
// frozen-params encoding measurements are judged against: a per-source
// re-encode saving only matters in proportion to this whole exchange.
func BenchmarkExchangeEndToEnd(b *testing.B) {
	for _, size := range []int{64, 512, 131072} {
		node := rpctest.NewNode(1)
		node.Return("eth_sendRawTransaction", "0x"+"ab")
		t := newTransport(node.URL(), DefaultBodyLimit)
		params := benchParams(size)
		ctx := context.Background()
		b.Run(fmt.Sprintf("params-%dB", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var result json.RawMessage
				if err := t.Call(ctx, "eth_sendRawTransaction", params...).Decode(&result); err != nil {
					b.Fatal(err)
				}
			}
		})
		node.Close()
	}
}

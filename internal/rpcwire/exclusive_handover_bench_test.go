package rpcwire

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// benchBody builds one valid success response whose result is a receipt-like
// object padded to roughly size bytes, the shape the pool's read path hands
// to CompleteCall after an exchange.
func benchBody(size int) []byte {
	inner := strings.Repeat("x", max(0, size-96))
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"result":{"status":"0x1","blockNumber":"0x1b4","gasUsed":"0x5208","logs":[{"address":"0xabc","topics":["0xd"],"data":"%s"}]}}`, inner)
	return []byte(body)
}

// BenchmarkCompleteCallDelivery measures the terminal delivery of one
// completed exchange into a *json.RawMessage target — the shape every pool
// read takes — across result sizes. The delivery consumes the freshly
// decoded result in place (one copy into the target); the envelope, id, and
// error validation are unchanged from the shared classification.
func BenchmarkCompleteCallDelivery(b *testing.B) {
	requestID := json.RawMessage(`7`)
	for _, size := range []int{64, 1024, 4096} {
		body := benchBody(size)
		b.Run(fmt.Sprintf("raw-target-%dB", size), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for range b.N {
				var target json.RawMessage
				if err := CompleteCall(body, requestID, rpc.Exchange{Progress: rpc.ExchangeCompleted}).Decode(&target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecodeResponsePublicConsumption measures the public decode for a
// consumer that keeps the Response alive and may re-read it: the id and
// result clones are the repeatable-consumption isolation contract, not
// delivery overhead.
func BenchmarkDecodeResponsePublicConsumption(b *testing.B) {
	requestID := json.RawMessage(`7`)
	body := benchBody(1024)
	b.ReportAllocs()
	for range b.N {
		resp, err := DecodeResponse(body, requestID)
		if err != nil || resp.Error != nil {
			b.Fatalf("decode: %v", err)
		}
		if len(resp.Result) == 0 {
			b.Fatal("empty result")
		}
	}
}

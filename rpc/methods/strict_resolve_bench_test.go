package methods

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/rpc"
)

// BenchmarkStrictResolveReceipt measures the clustering path a canonical
// method actually runs, and it builds both wirings here rather than reading
// them off the real policy, so the comparison cannot drift when a method is
// rewired later. The normalizer and the encoder are the same two functions in
// both sub-benchmarks; only the option differs, which is the whole of 0047.
//
// The narrow-key sub-benchmark is the shape a block method will have once it
// is wired, where the key stays the normalizer's and the value is still the
// encoder's output. It is kept here so the cost of that decision is a number
// in the repository rather than a figure quoted from a throwaway run.
func BenchmarkStrictResolveReceipt(b *testing.B) {
	raw := benchmarkReceipt(b)
	normalize := func(value json.RawMessage) (string, error) {
		return normalizeReceiptObject(value)
	}
	encode := receipt.Encode
	observations := []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{Name: "a", ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "c", ID: "c"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}}},
	}
	wirings := []struct {
		name string
		opt  rpc.StrictOption
	}{
		{"narrow-key", rpc.WithCanonicalObject(encode)},
		{"canonical-bytes", rpc.WithCanonicalValue(encode)},
	}
	for _, wiring := range wirings {
		b.Run(wiring.name, func(b *testing.B) {
			policy := rpc.Strict(normalize, wiring.opt, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, observations)
				if err != nil || decision.Decision.Status != rpc.Corroborated {
					b.Fatalf("decision = %+v err %v", decision, err)
				}
			}
		})
	}
}

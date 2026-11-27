package methods

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// benchmarkTransactionRecord loads the recorded mainnet transaction once for
// the normalization benchmarks below.
func benchmarkTransactionRecord(t testing.TB) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/transaction/anvil-1.7.1-pending.json")
	if err == nil {
		var fixture struct {
			Response string `json:"response"`
		}
		if err := json.Unmarshal(data, &fixture); err == nil {
			var envelope struct {
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal([]byte(fixture.Response), &envelope); err == nil {
				return envelope.Result
			}
		}
	}
	return nil
}

// BenchmarkTransactionByHashNormalize measures the full per-source
// preparation the comparison key pays: safety validation, whole-key
// corrections over the read-only original, the official-mask prune, the
// typed schema decode, and the canonical merge.
func BenchmarkTransactionByHashNormalize(b *testing.B) {
	raw := benchmarkTransactionRecord(b)
	if raw == nil {
		b.Skip("no recorded transaction fixture")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := normalizeTransactionByHash(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTransactionByHashAdmit measures the admission half: preparation
// plus the request-bound object validation the voter runs before its vote.
func BenchmarkTransactionByHashAdmit(b *testing.B) {
	raw := benchmarkTransactionRecord(b)
	if raw == nil {
		b.Skip("no recorded transaction fixture")
	}
	var fields struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		b.Fatal(err)
	}
	request := rpc.Request{Method: MethodGetTransactionByHash, Params: []any{fields.Hash}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := admitTransactionByHash(context.Background(), request, raw, nil); err != nil {
			b.Fatal(err)
		}
	}
}

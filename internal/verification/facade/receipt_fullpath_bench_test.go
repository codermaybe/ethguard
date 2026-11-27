package ethguard_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
)

// BenchmarkTransactionReceiptProductionPath includes the typed client, three
// real local HTTP sources, admission, resolution and final public projection.
// Reusing the existing fleet and receipt fixtures makes this file usable on
// the prior version too. These timings do not model provider network latency.
func BenchmarkTransactionReceiptProductionPath(b *testing.B) {
	for _, count := range []int{1, 64} {
		b.Run(fmt.Sprintf("sources-3/logs-%d", count), func(b *testing.B) {
			hash := ethapi.Hash("0x" + strings.Repeat("42", 32))
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(fixtureReceipt(b, hash, 16), &fields); err != nil {
				b.Fatal(err)
			}
			var originalRows []map[string]json.RawMessage
			if err := json.Unmarshal(fields["logs"], &originalRows); err != nil {
				b.Fatal(err)
			}
			rows := make([]json.RawMessage, count)
			for i := range rows {
				originalRows[0]["logIndex"] = json.RawMessage(fmt.Sprintf(`"0x%x"`, i))
				var err error
				rows[i], err = json.Marshal(originalRows[0])
				if err != nil {
					b.Fatal(err)
				}
			}
			// Repeating the same address/topics leaves the bloom unchanged.
			var err error
			fields["logs"], err = json.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				b.Fatal(err)
			}
			client, nodes := benchFleet(b, 3)
			for _, node := range nodes {
				node.ReturnRaw(methods.MethodGetTransactionReceipt, raw)
			}
			warm, result, err := client.TransactionReceipt(b.Context(), hash)
			if err != nil || len(warm.Logs) != count || result.Decision.Status != rpc.Corroborated {
				b.Fatalf("warm receipt = %+v, %+v, %v", warm, result, err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for range b.N {
				value, result, err := client.TransactionReceipt(b.Context(), hash)
				if err != nil || len(value.Logs) != count || result.Decision.Status != rpc.Corroborated {
					b.Fatalf("receipt = %+v, %+v, %v", value, result, err)
				}
			}
			b.StopTimer()
			requests := 0
			for _, node := range nodes {
				for _, call := range node.Calls() {
					if call.Method == methods.MethodGetTransactionReceipt {
						requests++
					}
				}
			}
			if requests != 3*(b.N+1) {
				b.Fatalf("receipt request count = %d, want %d", requests, 3*(b.N+1))
			}
			b.ReportMetric(float64(requests-3)/float64(b.N), "requests/op")
		})
	}
}

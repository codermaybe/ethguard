package log

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// benchLogRows builds one getLogs-shaped response with n full log objects,
// the shape a large historical range read admits per source. The row bytes
// are prepared and decoded once; the timed loop runs the kernel on the
// already-decoded rows, so fixture construction stays out of the measurement.
func benchLogRows(b *testing.B, n int) []json.RawMessage {
	b.Helper()
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"address":"0x0000000000000000000000000000000000000abc","topics":["0x`+strings.Repeat("11", 32)+`","0x`+strings.Repeat("22", 32)+`"],"data":"0x`+strings.Repeat("ab", 128)+`","blockNumber":"0x%x","transactionHash":"0x`+strings.Repeat("33", 32)+`","transactionIndex":"0x2a","blockHash":"0x`+strings.Repeat("44", 32)+`","logIndex":"0x%x","removed":false,"blockTimestamp":"0x652f0c1e"}`, 0x1b2f+i, i)
	}
	decoded := make([]json.RawMessage, n)
	for i, row := range rows {
		if err := json.Unmarshal([]byte(row), &decoded[i]); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(strings.Join(rows, ","))))
	return decoded
}

func BenchmarkLogArrayValidate(b *testing.B) {
	for _, n := range []int{1, 64} {
		b.Run(fmt.Sprintf("logs-%d", n), func(b *testing.B) {
			rows := benchLogRows(b, n)
			b.ResetTimer()
			for range b.N {
				for _, row := range rows {
					if err := Validate(row); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkLogArrayEncode(b *testing.B) {
	for _, n := range []int{1, 64} {
		b.Run(fmt.Sprintf("logs-%d", n), func(b *testing.B) {
			rows := benchLogRows(b, n)
			b.ResetTimer()
			for range b.N {
				for _, row := range rows {
					if _, err := EncodeOne(row); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// BenchmarkLogResultEncode includes the array safety gate, per-row encoding
// and final array serialization. The row benchmarks above isolate only the
// per-item work and do not measure this complete result path.
func BenchmarkLogResultEncode(b *testing.B) {
	for _, n := range []int{0, 1, 64} {
		b.Run(fmt.Sprintf("logs-%d", n), func(b *testing.B) {
			rows := benchLogRows(b, n)
			raw, err := json.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := Encode(raw); err != nil {
				b.Fatalf("invalid fixture: %v", err)
			}
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := Encode(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

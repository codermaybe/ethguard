package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// BenchmarkLogsAdmission measures the real admission hook, including response
// array parsing, row validation and binding every row to a valid range filter.
func BenchmarkLogsAdmission(b *testing.B) {
	for _, count := range []int{1, 64} {
		b.Run(fmt.Sprintf("rows-%d", count), func(b *testing.B) {
			row := json.RawMessage(logEntry("0x0000000000000000000000000000000000000001", "0x"+strings.Repeat("ab", 128), "0xa", "false", `"blockTimestamp":"0x652f0c1e"`))
			rows := make([]json.RawMessage, count)
			for i := range rows {
				rows[i] = row
			}
			value, err := json.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			req := rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{"fromBlock": "0x0", "toBlock": "0x10"}}}
			ctx := context.Background()
			if err := validateLogRequest(req); err != nil {
				b.Fatal(err)
			}
			if _, err := admitLogs(ctx, req, value, nil); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := admitLogs(ctx, req, value, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

package log

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	objectlog "github.com/codermaybe/ethguard/internal/ethobject/log"
)

// benchLogs builds one getLogs-shaped response with n full log objects, the
// shape a large historical range read admits per source.
func benchLogs(n int) json.RawMessage {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"address":"0x0000000000000000000000000000000000000abc","topics":["0x` + strings.Repeat("11", 32) + `","0x` + strings.Repeat("22", 32) + `"],"data":"0x` + strings.Repeat("ab", 128) + `","blockNumber":"0x%x","transactionHash":"0x` + strings.Repeat("33", 32) + `","transactionIndex":"0x2a","blockHash":"0x` + strings.Repeat("44", 32) + `","logIndex":"0x%x","removed":false,"blockTimestamp":"0x652f0c1e"}`, 0x1b2f+i, i)
	}
	return json.RawMessage(`[` + strings.Join(rows, ",") + `]`)
}

func BenchmarkLogArrayValidate(b *testing.B) {
	value := benchLogs(64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rows := []json.RawMessage{}
		if err := json.Unmarshal(value, &rows); err != nil {
			b.Fatal(err)
		}
		for _, row := range rows {
			if err := objectlog.Validate(row); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkLogArrayEncode(b *testing.B) {
	value := benchLogs(64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Encode(value); err != nil {
			b.Fatal(err)
		}
	}
}

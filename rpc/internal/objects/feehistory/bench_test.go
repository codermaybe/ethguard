package feehistory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// benchFeeHistory builds one feeHistory-shaped response over n blocks with
// reward rows, the shape a corroborated range read admits per source.
func benchFeeHistory(n int) json.RawMessage {
	base := make([]string, n+1)
	blob := make([]string, n+1)
	gas := make([]string, n)
	rows := make([]string, n)
	for i := 0; i < n; i++ {
		base[i] = fmt.Sprintf(`"0x%x"`, 30_000_000_000+i)
		blob[i] = fmt.Sprintf(`"0x%x"`, i+1)
		gas[i] = fmt.Sprintf(`0.%03d`, 100+(i%800))
		sample := make([]string, 2)
		sample[0] = fmt.Sprintf(`"0x%x"`, 1_000_000_000+i)
		sample[1] = fmt.Sprintf(`"0x%x"`, 2_000_000_000+i)
		rows[i] = "[" + strings.Join(sample, ",") + "]"
	}
	base[n] = `"0x1d1a94a200"`
	blob[n] = `"0x2"`
	return json.RawMessage(fmt.Sprintf(
		`{"oldestBlock":"0x1200000","baseFeePerGas":[%s],"gasUsedRatio":[%s],"reward":[%s],"baseFeePerBlobGas":[%s],"blobGasUsedRatio":[%s]}`,
		strings.Join(base, ","), strings.Join(gas, ","), strings.Join(rows, ","), strings.Join(blob, ","), strings.Join(gas, ",")))
}

func BenchmarkFeeHistoryNormalize(b *testing.B) {
	value := benchFeeHistory(64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Normalize(value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFeeHistoryEncode(b *testing.B) {
	value := benchFeeHistory(64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Encode(value); err != nil {
			b.Fatal(err)
		}
	}
}

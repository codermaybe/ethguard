package uncertain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The retention baseline for the material-only diagnostic path: what one
// recorded response costs when a call opts into source evidence, measured
// separately from the business pipeline that produces the response. The
// object row exercises the full path — raw retention plus the bounded
// top-level field expansion; the scalar row is the raw-only shape. The
// snapshot row adds the delivery-time defensive copy so the retention cost
// and the snapshot cost stay attributable.

// retentionResponseFixture builds one realistic response body of roughly
// targetSize bytes: an object with several top-level members, so Record
// retains the raw prefix and expands the complete members into Fields.
func retentionResponseFixture(b *testing.B, targetSize int) []byte {
	b.Helper()
	object := map[string]any{
		"number":     "0x10",
		"hash":       "0x" + strings.Repeat("11", 32),
		"parentHash": "0x" + strings.Repeat("22", 32),
		"stateRoot":  "0x" + strings.Repeat("33", 32),
		"gasLimit":   "0x1c9c380",
		"gasUsed":    "0x5208",
		"timestamp":  "0x5",
		"miner":      "0x" + strings.Repeat("44", 20),
	}
	raw, err := json.Marshal(object)
	if err != nil {
		b.Fatal(err)
	}
	// Pad with an extraData member to reach the target size so the item cap
	// stays out of play and the measurement is the steady-state cost.
	padding := targetSize - len(raw) - len(`,"extraData":""`)
	if padding > 0 {
		object["extraData"] = "0x" + strings.Repeat("ab", padding/2)
		raw, err = json.Marshal(object)
		if err != nil {
			b.Fatal(err)
		}
	}
	return raw
}

func benchmarkRetentionRecord(b *testing.B, targetSize int, withSnapshot bool) {
	raw := retentionResponseFixture(b, targetSize)
	cfg := Config{BudgetBytes: 1 << 20}
	source := rpc.SourceRef{Name: "node-a"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector := NewCollector(cfg)
		collector.Record("response", "eth_getBlockByNumber", source, raw, "")
		if withSnapshot {
			snapshot := collector.Snapshot()
			if len(snapshot.Raw) != 1 {
				b.Fatal("record was not retained")
			}
		}
	}
}

func BenchmarkRetentionRecordObjectResponse(b *testing.B) {
	benchmarkRetentionRecord(b, 4<<10, false)
}

func BenchmarkRetentionRecordScalarResponse(b *testing.B) {
	benchmarkRetentionRecord(b, 16, false)
}

func BenchmarkRetentionRecordAndSnapshot(b *testing.B) {
	benchmarkRetentionRecord(b, 4<<10, true)
}

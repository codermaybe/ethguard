package receipt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/ethobject/log"
)

// The former implementation stays here as an independent geth oracle and
// allocation comparison; it reconstructs the same address/topic tree.
func referenceCanonicalBloom(logs []log.CanonicalLog) string {
	decoded := make([]*types.Log, len(logs))
	for i, item := range logs {
		entry := &types.Log{Address: common.HexToAddress(item.Address)}
		entry.Topics = make([]common.Hash, len(item.Topics))
		for j, topic := range item.Topics {
			entry.Topics[j] = common.HexToHash(topic)
		}
		decoded[i] = entry
	}
	return hexutil.Encode(types.CreateBloom(&types.Receipt{Logs: decoded}).Bytes())
}

func bloomCostLogs(count int) []log.CanonicalLog {
	rows := make([]log.CanonicalLog, count)
	for i := range rows {
		rows[i].Address = fmt.Sprintf("0x%040x", i%7)
		for j := 0; j < i%5; j++ {
			rows[i].Topics = append(rows[i].Topics, fmt.Sprintf("0x%064x", (i+j)%13))
		}
	}
	return rows
}

func TestCanonicalBloomMatchesGethTree(t *testing.T) {
	for _, count := range []int{0, 1, 4, 100, 1000} {
		rows := bloomCostLogs(count)
		if got, want := canonicalBloom(rows), referenceCanonicalBloom(rows); got != want {
			t.Fatalf("%d logs: bloom = %s, want %s", count, got, want)
		}
	}
	rows := []log.CanonicalLog{{Address: "0x" + strings.Repeat("ff", 20), Topics: []string{"0x" + strings.Repeat("ff", 32)}}}
	if got, want := canonicalBloom(rows), referenceCanonicalBloom(rows); got != want {
		t.Fatalf("high-byte bloom = %s, want %s", got, want)
	}
}

var bloomCostSink string

func BenchmarkCanonicalBloom(b *testing.B) {
	for _, count := range []int{0, 1, 100, 1000} {
		b.Run(fmt.Sprintf("logs-%d", count), func(b *testing.B) {
			rows := bloomCostLogs(count)
			for _, implementation := range []struct {
				name string
				run  func([]log.CanonicalLog) string
			}{{"production", canonicalBloom}, {"reference-tree", referenceCanonicalBloom}} {
				b.Run(implementation.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						bloomCostSink = implementation.run(rows)
					}
				})
			}
		})
	}
}

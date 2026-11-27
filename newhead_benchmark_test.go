package ethguard

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	sub "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/ethereum/go-ethereum/core/types"
)

func BenchmarkHeadDecode(b *testing.B) {
	normal := testHeader(&types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(1)})
	var extra strings.Builder
	extra.Write(normal[:len(normal)-1])
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&extra, ",\"u%d\":0", i)
	}
	extra.WriteByte('}')
	for _, tc := range []struct {
		name string
		raw  []byte
	}{{"normal", normal}, {"4000_short_unknown_fields", []byte(extra.String())}} {
		b.Run(tc.name, func(b *testing.B) {
			budget := sub.NewMemoryBudget(64 << 20)
			s := &HeadSubscription{budget: budget, arena: sub.NewPayloadArena(budget), policy: HeadSubscriptionPolicy{MaxMessageBytes: 1 << 20}}
			b.ReportAllocs()
			for b.Loop() {
				_, lease, err := s.decode(tc.raw)
				if err != nil {
					b.Fatal(err)
				}
				lease.Release()
			}
			b.ReportMetric(float64(len(tc.raw)), "wire_B")
			b.ReportMetric(float64(len(tc.raw)*256+(16<<10)), "decode_reserved_B")
			if budget.Used() != 0 {
				b.Fatal("decode leaked budget")
			}
		})
	}
}

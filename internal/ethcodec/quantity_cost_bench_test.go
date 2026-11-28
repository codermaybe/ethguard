package ethcodec

import (
	"encoding/json"
	"testing"
)

// The quantity preparation cost witness. The sampling and local resolvers
// (median, local) normalize every answer once and then encode the selected
// or median value once more, and the quantity encoders run the same wire
// normalization internally — so one answer's comparison work is one
// normalization and the representative's commit work is a second. This
// benchmark measures the unit that duplication costs, so the sampling
// families' keep-the-route decisions carry a measured justification instead
// of an assumption. It measures the codec unit, not a resolver or network
// path; no per-family cost conclusion is drawn from it alone.

func BenchmarkQuantityPrepare(b *testing.B) {
	for _, unit := range []struct {
		name  string
		input string
	}{
		{"short", `"0x1b7"`},
		{"typical", `"0x1b7a3d200"`},
		{"long", `"0x1b7a3d2000000000000000000000000"`},
	} {
		b.Run(unit.name, func(b *testing.B) {
			value := json.RawMessage(unit.input)
			b.ReportAllocs()
			b.Run("normalize", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := NormalizeWireQuantity(value); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("encode", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := EncodeQuantity(value); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("encode-folded", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := EncodeFoldedQuantity(value); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

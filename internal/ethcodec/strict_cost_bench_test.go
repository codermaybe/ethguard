package ethcodec

import (
	"encoding/json"
	"strings"
	"testing"
)

// These benchmarks use only entry points present before the ASCII change so
// the same file can be compiled against both source revisions.
func BenchmarkStrictScalarValidation(b *testing.B) {
	for _, scalar := range []struct {
		name      string
		predicate func(string) bool
		valid     string
		large     string
	}{
		{"Quantity", IsCanonicalQuantity, "0x1b7a3d200", "0x" + strings.Repeat("a", 4096)},
		{"Data", IsWholeByteData, "0x0123456789abcdef", "0x" + strings.Repeat("ab", 2048)},
		{"DecimalString", IsCanonicalDecimal, "12345678901234567890", "1" + strings.Repeat("9", 4096)},
	} {
		b.Run(scalar.name, func(b *testing.B) {
			for _, fixture := range []struct {
				name  string
				text  string
				valid bool
			}{
				{"Typical", scalar.valid, true},
				{"Large", scalar.large, true},
				{"InvalidFirst", "g" + scalar.large[1:], false},
				{"InvalidLast", scalar.large[:len(scalar.large)-1] + "g", false},
			} {
				b.Run(fixture.name, func(b *testing.B) {
					b.SetBytes(int64(len(fixture.text)))
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if got := scalar.predicate(fixture.text); got != fixture.valid {
							b.Fatalf("valid = %v, want %v", got, fixture.valid)
						}
					}
				})
			}
		})
	}
}

func BenchmarkCanonicalQuantity(b *testing.B) {
	for _, fixture := range []struct {
		name  string
		raw   json.RawMessage
		valid bool
	}{
		{"Typical", json.RawMessage(`"0x1b7a3d200"`), true},
		{"Uppercase", json.RawMessage(`"0X1B7A3D200"`), true},
		{"Escaped", json.RawMessage(`"\u0030x1b7a3d200"`), true},
		{"Large", json.RawMessage(`"0x` + strings.Repeat("a", 4096) + `"`), true},
		{"InvalidLast", json.RawMessage(`"0x` + strings.Repeat("a", 4095) + `g"`), false},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			b.SetBytes(int64(len(fixture.raw)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := CanonicalQuantity(fixture.raw); (err == nil) != fixture.valid {
					b.Fatalf("error = %v, want valid=%v", err, fixture.valid)
				}
			}
		})
	}
}

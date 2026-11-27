package ethapi_test

import (
	"encoding/json"
	"strings"
	"testing"
)

type fixedHexBenchmarkCase struct {
	name  string
	raw   json.RawMessage
	valid bool
}

var fixedHexBenchmarkValue string

func BenchmarkFixedHexConstructors(b *testing.B) {
	for _, constructor := range fixedHexConstructors {
		b.Run(constructor.name, func(b *testing.B) {
			for _, fixture := range fixedHexBenchmarkCases(constructor) {
				b.Run(fixture.name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(fixture.raw)))
					b.ResetTimer()
					for range b.N {
						value, err := constructor.parse(fixture.raw)
						if (err == nil) != fixture.valid {
							b.Fatalf("constructor error = %v, want valid=%v", err, fixture.valid)
						}
						fixedHexBenchmarkValue = value
					}
				})
			}
		})
	}
}

// Fixture construction is outside timers. Long invalid strings retain the
// public constructor's decoding and formatted-error costs in both revisions.
func fixedHexBenchmarkCases(constructor fixedHexConstructor) []fixedHexBenchmarkCase {
	digits := strings.Repeat("a", constructor.digits)
	return []fixedHexBenchmarkCase{
		{name: "Plain", raw: json.RawMessage(`"0x` + digits + `"`), valid: true},
		{name: "Escaped", raw: json.RawMessage(`"\u0030x` + digits + `"`), valid: true},
		{name: "InvalidFirstDigit", raw: json.RawMessage(`"0xg` + digits[1:] + `"`)},
		{name: "InvalidLastDigit", raw: json.RawMessage(`"0x` + digits[:len(digits)-1] + `g"`)},
		{name: "Short", raw: json.RawMessage(`"0x` + digits[:len(digits)-1] + `"`)},
		{name: "Long", raw: json.RawMessage(`"0x` + digits + `a"`)},
		{name: "VeryLong", raw: json.RawMessage(`"0x` + strings.Repeat("a", 64<<10) + `"`)},
	}
}

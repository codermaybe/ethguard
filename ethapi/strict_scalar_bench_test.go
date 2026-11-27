package ethapi_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

var strictScalarBenchmarkValue string

// This benchmark uses only constructors present before the ASCII change.
// Copy both strict_scalar_*_test.go files to the baseline for identical work.
func BenchmarkStrictScalarConstructors(b *testing.B) {
	for _, constructor := range strictScalarConstructors {
		b.Run(constructor.name, func(b *testing.B) {
			for _, fixture := range strictScalarBenchmarkCases(constructor) {
				b.Run(fixture.name, func(b *testing.B) {
					_, referenceErr := constructor.reference(fixture.raw)
					valid := referenceErr == nil
					b.ReportAllocs()
					b.SetBytes(int64(len(fixture.raw)))
					b.ResetTimer()
					for range b.N {
						value, err := constructor.parse(fixture.raw)
						if (err == nil) != valid {
							b.Fatalf("constructor error = %v, want valid=%v", err, valid)
						}
						strictScalarBenchmarkValue = value
					}
				})
			}
		})
	}
}

type strictScalarBenchmarkCase struct {
	name string
	raw  json.RawMessage
}

func strictScalarBenchmarkCases(constructor strictScalarConstructor) []strictScalarBenchmarkCase {
	text := constructor.sample
	prefix, digit := "0x", "a"
	if constructor.name == "DecimalString" {
		prefix, digit = "", "1"
	}
	first := len(prefix)
	return []strictScalarBenchmarkCase{
		{"Plain", json.RawMessage(`"` + text + `"`)},
		{"Escaped", json.RawMessage(fmt.Sprintf(`"\u%04x%s"`, text[0], text[1:]))},
		{"Short", json.RawMessage(`"` + prefix + `0"`)},
		{"Long", json.RawMessage(`"` + prefix + strings.Repeat(digit, 4096) + `"`)},
		{"InvalidFirstDigit", json.RawMessage(`"` + text[:first] + `g` + text[first+1:] + `"`)},
		{"InvalidLastDigit", json.RawMessage(`"` + text[:len(text)-1] + `g"`)},
	}
}

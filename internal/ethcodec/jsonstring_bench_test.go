package ethcodec

import (
	"encoding/json"
	"strings"
	"testing"
)

var jsonStringBenchmarkValue string
var jsonStringBenchmarkError error

// Both implementations receive the same input. Long failures measure the
// cost of falling back after a scan; no timing claim is asserted by a test.
func BenchmarkDecodeJSONString(b *testing.B) {
	long := strings.Repeat("a", 64<<10)
	for _, fixture := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"Plain", []byte(`"0x0123456789abcdef"`)},
		{"Escaped", []byte(`"\u0030x0123456789abcdef"`)},
		{"Unicode", []byte(`"中文🙂"`)},
		{"InvalidUTF8", []byte{'"', 0xff, '"'}},
		{"Plain64KiB", []byte(`"` + long + `"`)},
		{"ControlFirst64KiB", []byte("\"\x00" + long + "\"")},
		{"ControlLast64KiB", []byte("\"" + long + "\x00\"")},
		{"BadEscapeLast64KiB", []byte(`"` + long + `\q"`)},
		{"Trailing64KiB", []byte(`"` + long + `" "tail"`)},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			checkJSONString(b, fixture.raw)
			for _, decoder := range []struct {
				name string
				call func(json.RawMessage) (string, error)
			}{
				{"Standard", standardJSONString},
				{"Fast", DecodeJSONString},
			} {
				b.Run(decoder.name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(fixture.raw)))
					b.ResetTimer()
					for range b.N {
						jsonStringBenchmarkValue, jsonStringBenchmarkError = decoder.call(fixture.raw)
					}
				})
			}
		})
	}
}

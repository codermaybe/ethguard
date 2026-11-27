package ethcodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func referenceMarshalNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func TestMarshalNoEscapePreservesEncoderBytes(t *testing.T) {
	values := []any{
		"", "0x0123456789abcdef", "<>&/", "already\\u003c",
		"quoted\"text", "back\\slash", "\x00\b\f\n\r\t\x1f\x7f",
		"中文🙂", "\u2028\u2029", string([]byte{0xff, 0xc0, 0x80}),
		json.Number("9007199254740993"), json.RawMessage(`{"kept":null}`),
		[]string{"<>&", "\n", "中文"}, nil,
	}
	for _, n := range []int{127, 128, 129, 4096} {
		values = append(values, strings.Repeat("a", n), strings.Repeat("a", n)+`"`)
	}
	// Every byte exercises the fast-path boundary, control escaping, and
	// invalid UTF-8 handling against the independent standard encoder.
	for b := 0; b < 256; b++ {
		values = append(values, "prefix"+string([]byte{byte(b)})+"suffix")
	}
	for i, value := range values {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			want, wantErr := referenceMarshalNoEscape(value)
			got, err := MarshalNoEscape(value)
			if err != nil || wantErr != nil || !bytes.Equal(got, want) {
				t.Fatalf("value=%q: got %q, %v; want %q, %v", value, got, err, want, wantErr)
			}
		})
	}
}

func FuzzMarshalNoEscapeString(f *testing.F) {
	for _, seed := range []string{"", "0xabcdef", "<>&/", "\\u003c", "\n\"\\", "中文\u2028", string([]byte{0xff})} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		text := string(input)
		want, wantErr := referenceMarshalNoEscape(text)
		got, err := MarshalNoEscape(text)
		if err != nil || wantErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("input=%q: got %q, %v; want %q, %v", input, got, err, want, wantErr)
		}
	})
}

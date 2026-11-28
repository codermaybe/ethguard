package ethcodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// This is the complete former string decode, independent of the fast path.
func standardJSONString(raw json.RawMessage) (string, error) {
	var text string
	err := json.Unmarshal(raw, &text)
	return text, err
}

func checkJSONString(t testing.TB, raw json.RawMessage) {
	t.Helper()
	want, wantErr := standardJSONString(raw)
	got, gotErr := DecodeJSONString(raw)
	// DeepEqual includes SyntaxError offsets and UnmarshalTypeError fields,
	// not just the public error text.
	if got != want || !reflect.DeepEqual(gotErr, wantErr) {
		t.Fatalf("decode (%d bytes) = %q, %T %v; standard = %q, %T %v", len(raw), got, gotErr, gotErr, want, wantErr, wantErr)
	}
}

func jsonStringInputs() []json.RawMessage {
	return []json.RawMessage{
		nil, {}, []byte(" "), []byte("null"), []byte(" \t\r\nnull\r\n"),
		[]byte("true"), []byte("42"), []byte("{}"), []byte("[]"), []byte(`["text"]`),
		[]byte(`""`), []byte(`"x"`), []byte(`"0x0123456789abcdef"`), []byte(`"<>& / ~"`),
		[]byte(`"\"\\\/\b\f\n\r\t"`), []byte(`"\u0000\u007f\u0080"`),
		[]byte(`"中文🙂"`), []byte(`"\ud83d\ude42"`), []byte(`"\ud800"`), []byte(`"\udc00"`),
		{'"', 0xff, '"'}, {'"', 0xc0, 0xaf, '"'}, {'"', 0xed, 0xa0, 0x80, '"'},
		[]byte(" \t\r\n\"plain\" \t\r\n"), []byte("\u00a0\"plain\""), []byte("\"plain\"\u00a0"),
		[]byte(`"one" "two"`), []byte(`"one" null`), []byte(`"one"?`), []byte(`"one`), []byte(`"`),
		[]byte(`"\q"`), []byte(`"\u12"`), []byte("\"one\n\""), []byte("\"one\x00\""),
	}
}

func TestDecodeJSONStringMatchesStandardJSON(t *testing.T) {
	for _, raw := range jsonStringInputs() {
		checkJSONString(t, raw)
	}
	for c := 0; c <= 255; c++ {
		// Raw mutation exercises invalid UTF-8 and controls without having a
		// fixture marshaler replace or escape them before the test.
		checkJSONString(t, []byte{'"', byte(c), '"'})
		checkJSONString(t, []byte{'"', 'a', byte(c), 'z', '"'})
	}
}

func TestDecodeJSONStringOwnsResult(t *testing.T) {
	for _, raw := range []json.RawMessage{
		[]byte(`""`), []byte(`"x"`), []byte(`"copied ASCII text"`),
		[]byte(`"escaped\u0020text"`), []byte(`"中文"`), {'"', 0xff, '"'},
	} {
		before := bytes.Clone(raw)
		want, wantErr := standardJSONString(raw)
		got, err := DecodeJSONString(raw)
		if err != nil || wantErr != nil || got != want {
			t.Fatalf("decode = %q / %v, want %q / %v", got, err, want, wantErr)
		}
		if !bytes.Equal(raw, before) {
			t.Fatal("decoder modified its input")
		}
		for i := range raw {
			raw[i] = '!'
		}
		if got != want {
			t.Fatalf("result aliases input: got %q, want %q", got, want)
		}
	}
}

func TestDecodeJSONStringLongInvalidMatchesStandardJSON(t *testing.T) {
	for _, size := range []int{4096, 64 << 10, 1 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			text := strings.Repeat("a", size)
			for _, raw := range []json.RawMessage{
				[]byte("\"\x00" + text + "\""),
				[]byte("\"" + text + "\x00\""),
				[]byte(`"` + text + `\q"`),
				[]byte(`"` + text + `" "tail"`),
				[]byte(`"` + text),
			} {
				checkJSONString(t, raw)
				if _, err := DecodeJSONString(raw); err == nil {
					t.Fatal("invalid long input accepted")
				}
			}
		})
	}
}

func FuzzDecodeJSONStringMatchesStandardJSON(f *testing.F) {
	for _, raw := range jsonStringInputs() {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		checkJSONString(t, raw)
	})
}

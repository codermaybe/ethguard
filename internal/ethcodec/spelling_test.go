package ethcodec

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFoldHexSpellingPreservesValueAndInvalidInputs(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"0X00AB"`, `"0x00ab"`},
		{`"0xF"`, `"0xf"`}, // Odd DATA remains odd for the later schema check.
		{`"0X0000"`, `"0x0000"`},
		{`"\u0030\u0058A\u0042"`, `"0xab"`},
		{`"0x\u0061b"`, `"0x\u0061b"`},
		{`"0x001a"`, `"0x001a"`},
		{`"0x"`, `"0x"`},
		{`"text 0XAB"`, `"text 0XAB"`},
		{`"0XAG"`, `"0XAG"`},
		{`"0XAB" false`, `"0XAB" false`},
		{`null`, `null`}, {`12`, `12`},
		{`["0XAB"]`, `["0XAB"]`},
		{`{"value":"0XAB"}`, `{"value":"0XAB"}`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			raw := json.RawMessage(tc.input)
			before := bytes.Clone(raw)
			got := FoldHexSpelling(raw)
			if string(got) != tc.want || !bytes.Equal(raw, before) {
				t.Fatalf("got %s, source %s; want %s without source mutation", got, raw, tc.want)
			}
			if tc.input == tc.want && &got[0] != &raw[0] {
				t.Fatal("unchanged spelling allocated a replacement")
			}
		})
	}
}

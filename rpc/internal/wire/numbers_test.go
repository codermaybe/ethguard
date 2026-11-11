package wire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func TestCanonicalHexByte(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "zero", input: `"0x0"`, want: "0x0"},
		{name: "padded zero", input: `"0x00"`, want: "0x0"},
		{name: "single digit", input: `"0x4"`, want: "0x4"},
		{name: "padded digit", input: `"0x04"`, want: "0x4"},
		{name: "uppercase digit", input: `"0xA"`, want: "0xa"},
		{name: "mixed case", input: `"0xaF"`, want: "0xaf"},
		{name: "maximum", input: `"0xFF"`, want: "0xff"},
		{name: "JSON whitespace", input: " \n\t\"0x10\"\r ", want: "0x10"},
		{name: "JSON escape", input: `"0\u00784A"`, want: "0x4a"},
		{name: "empty input", input: ""},
		{name: "null", input: `null`},
		{name: "number", input: `12`},
		{name: "boolean", input: `true`},
		{name: "array", input: `["0x1"]`},
		{name: "object", input: `{}`},
		{name: "empty string", input: `""`},
		{name: "no digits", input: `"0x"`},
		{name: "no prefix", input: `"ff"`},
		{name: "uppercase prefix", input: `"0Xff"`, want: "0xff"},
		{name: "uppercase prefix overflow", input: `"0X100"`},
		{name: "uppercase prefix invalid digit", input: `"0Xg1"`},
		{name: "three digits", input: `"0x100"`},
		{name: "three zero digits", input: `"0x000"`},
		{name: "plus sign", input: `"0x+1"`},
		{name: "minus sign", input: `"0x-1"`},
		{name: "invalid digit", input: `"0xg1"`},
		{name: "internal whitespace", input: `"0x 1"`},
		{name: "unterminated string", input: `"0x1`},
		{name: "second JSON value", input: `"0x1" null`},
		{name: "adjacent JSON value", input: `"0x1""0x2"`},
		{name: "malformed trailing input", input: `"0x1" garbage`},
		{name: "trailing punctuation", input: `"0x1",`},
		{name: "non JSON whitespace", input: "\v\"0x1\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wire.CanonicalHexByte(json.RawMessage(tt.input))
			if tt.want == "" {
				if err == nil {
					t.Fatalf("CanonicalHexByte(%q) = %q, want an error", tt.input, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("CanonicalHexByte(%q) = %q, %v; want %q, nil", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestCanonicalRatio(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "zero", input: `0`, want: "0"},
		{name: "one", input: `1`, want: "1"},
		{name: "trailing zeros", input: `0.50`, want: "0.5"},
		{name: "decimal one", input: `1.000`, want: "1"},
		{name: "exponent", input: `1e-1`, want: "0.1"},
		{name: "uppercase exponent", input: `5E-1`, want: "0.5"},
		{name: "positive exponent", input: `0.05e+1`, want: "0.5"},
		{name: "shifted decimal significand", input: `1.25e-1`, want: "0.125"},
		{name: "leading exponent zeros", input: `1e-000001`, want: "0.1"},
		{name: "small fixed decimal", input: `1e-6`, want: "0.000001"},
		{name: "near one", input: `0.9999999999999999`, want: "0.9999999999999999"},
		{name: "JSON whitespace", input: " \n\t0.50\r ", want: "0.5"},
		{name: "negative zero", input: `-0`, want: "0"},
		{name: "negative decimal zero", input: `-0.000`, want: "0"},
		{name: "negative exponent zero", input: `-0e+10`, want: "0"},
		{name: "large positive exponent zero", input: `0e1000000000`, want: "0"},
		{name: "large negative exponent zero", input: `0e-1000000000`, want: "0"},
		{name: "long coefficient with balancing exponent", input: "1" + strings.Repeat("0", 400) + "e-400", want: "1"},
		{name: "empty input", input: ""},
		{name: "whitespace only", input: " \n\t"},
		{name: "null", input: `null`},
		{name: "numeric string", input: `"0.5"`},
		{name: "escaped numeric string", input: `"\u0030.5"`},
		{name: "boolean", input: `true`},
		{name: "array", input: `[0.5]`},
		{name: "object", input: `{}`},
		{name: "negative", input: `-0.1`},
		{name: "greater than one", input: `1.1`},
		{name: "rounded down to one", input: `1.0000000000000001`},
		{name: "decimal precision loss", input: `0.10000000000000001`},
		{name: "near one precision loss", input: `0.99999999999999999`},
		{name: "positive underflow", input: `1e-1000000000`},
		{name: "negative underflow", input: `-1e-1000000000`},
		{name: "overflow", input: `1e1000000000`},
		{name: "leading plus", input: `+0.5`},
		{name: "leading zero", input: `00.5`},
		{name: "missing integer part", input: `.5`},
		{name: "missing fraction", input: `0.`},
		{name: "missing exponent", input: `0.5e`},
		{name: "NaN", input: `NaN`},
		{name: "infinity", input: `Infinity`},
		{name: "second JSON value", input: `0.5 null`},
		{name: "adjacent JSON value", input: `0.5"x"`},
		{name: "malformed trailing input", input: `0.5 garbage`},
		{name: "trailing punctuation", input: `0.5,`},
		{name: "trailing comment", input: `0.5/*comment*/`},
		{name: "non JSON whitespace", input: "\v0.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wire.CanonicalRatio(json.RawMessage(tt.input))
			if tt.want == "" {
				if err == nil {
					t.Fatalf("CanonicalRatio(%q) = %q, want an error", tt.input, got)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Fatalf("CanonicalRatio(%q) = %q, %v; want %q, nil", tt.input, got, err, tt.want)
			}
		})
	}
}

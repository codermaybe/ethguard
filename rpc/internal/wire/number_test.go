package wire_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func TestCanonicalNumber(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "zero", input: `0`, want: "0"},
		{name: "negative zero", input: `-0`, want: "0"},
		{name: "negative decimal zero", input: `-0.000e-123`, want: "0"},
		{name: "decimal trailing zeros", input: `0.50`, want: "0.5"},
		{name: "small exponent", input: `1e-1`, want: "0.1"},
		{name: "positive exponent", input: `1.250e+2`, want: "125"},
		{name: "integer padding", input: `1200.00`, want: "1200"},
		{name: "fraction padding", input: `0.001200`, want: "0.0012"},
		{name: "negative value", input: `-12.3400`, want: "-12.34"},
		{name: "no ratio upper bound", input: `2.50`, want: "2.5"},
		{name: "precision beyond float64", input: `0.30000000000000001`, want: "0.30000000000000001"},
		{name: "long precise fraction", input: `0.123456789012345678901234567890`, want: "0.12345678901234567890123456789"},
		{name: "large precise integer", input: `123456789012345678901234567890`, want: "1.2345678901234567890123456789e29"},
		{name: "JSON whitespace", input: " \n\t0.50\r ", want: "0.5"},
		{name: "uppercase exponent", input: `1.2300E1000000000`, want: "1.23e1000000000"},
		{name: "large negative exponent", input: `-1e-1000000000`, want: "-1e-1000000000"},
		{name: "large exponent zero", input: `0e1000000000`, want: "0"},
		{name: "large negative exponent zero", input: `-0.000e-1000000000`, want: "0"},
		{name: "exponent beyond machine integer", input: "1e" + strings.Repeat("9", 80), want: "1e" + strings.Repeat("9", 80)},
		{name: "exponent carry", input: "10e" + strings.Repeat("9", 80), want: "1e1" + strings.Repeat("0", 80)},
		{name: "arbitrarily large exponent zero", input: "-0e-" + strings.Repeat("9", 80), want: "0"},
		{name: "leading exponent zeros", input: "1e+" + strings.Repeat("0", 80) + "1", want: "10"},
		{name: "balanced long coefficient", input: "1" + strings.Repeat("0", 400) + "e-400", want: "1"},
		{name: "empty input", input: ""},
		{name: "whitespace only", input: " \n\t"},
		{name: "null", input: `null`},
		{name: "numeric string", input: `"0.5"`},
		{name: "escaped numeric string", input: `"\u0030.5"`},
		{name: "boolean", input: `true`},
		{name: "array", input: `[0.5]`},
		{name: "object", input: `{}`},
		{name: "leading plus", input: `+0.5`},
		{name: "leading zero", input: `00.5`},
		{name: "negative leading zero", input: `-01`},
		{name: "missing integer part", input: `.5`},
		{name: "missing fraction", input: `0.`},
		{name: "missing exponent", input: `0.5e`},
		{name: "missing exponent digits", input: `1e+`},
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
			got, err := wire.CanonicalNumber(json.RawMessage(tt.input))
			if tt.want == "" {
				if err == nil {
					t.Fatalf("CanonicalNumber(%q) = %q, want an error", tt.input, got)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Fatalf("CanonicalNumber(%q) = %q, %v; want %q, nil", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestCanonicalNumberEquivalentSpellings(t *testing.T) {
	groups := [][]string{
		{"0", "-0.00", "0e1000000000"},
		{"0.5", "0.50", "5e-1", "50e-2", "0.05e1"},
		{"-12.3", "-123e-1", "-0.12300e+2"},
		{"0.000001", "1e-6", "10e-7"},
		{"0.0000001", "1e-7", "0.1e-6"},
		{"100000000000000000000", "1e20", "10e19"},
		{"1000000000000000000000", "1e21", "10e20"},
	}
	for _, group := range groups {
		t.Run(group[0], func(t *testing.T) {
			var canonical json.Number
			for i, spelling := range group {
				got, err := wire.CanonicalNumber(json.RawMessage(spelling))
				if err != nil {
					t.Fatalf("CanonicalNumber(%q): %v", spelling, err)
				}
				if i == 0 {
					canonical = got
				} else if got != canonical {
					t.Fatalf("equivalent spelling %q canonicalized to %q, want %q", spelling, got, canonical)
				}
			}
		})
	}
}

func TestCompareNumbers(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "equal integers", a: `2`, b: `2`, want: 0},
		{name: "equal padded decimals", a: `0.50`, b: `5e-1`, want: 0},
		{name: "equal integer exponent", a: `1200`, b: `1.2e3`, want: 0},
		{name: "equal negative decimals", a: `-12.300`, b: `-0.123e2`, want: 0},
		{name: "signed zero", a: `-0`, b: `0.000`, want: 0},
		{name: "zero ignores huge exponent", a: "-0e-" + strings.Repeat("9", 80), b: `0e1000000000`, want: 0},
		{name: "positive lower boundary", a: `0`, b: `0.0000001`, want: -1},
		{name: "negative lower boundary", a: `-0.0000001`, b: `0`, want: -1},
		{name: "opposite signs", a: `-100`, b: `0.1`, want: -1},
		{name: "equal order differing digits", a: `1.23`, b: `1.24`, want: -1},
		{name: "implicit trailing zeros", a: `1.2`, b: `1.2001`, want: -1},
		{name: "fractional alignment", a: `0.0012`, b: `0.00119`, want: 1},
		{name: "different orders", a: `9.99`, b: `10`, want: -1},
		{name: "negative differing orders", a: `-10`, b: `-9.99`, want: -1},
		{name: "negative implicit zeros", a: `-1.2001`, b: `-1.2`, want: -1},
		{name: "precise fraction", a: `0.30000000000000001`, b: `0.3`, want: 1},
		{name: "precise integer", a: `9007199254740993`, b: `9007199254740992`, want: 1},
		{name: "slightly beyond one", a: `1.000000000000000000001`, b: `1`, want: 1},
		{name: "equal huge exponents", a: `1e1000000000`, b: `10e999999999`, want: 0},
		{name: "different huge exponents", a: `9e999999999`, b: `1e1000000000`, want: -1},
		{name: "different tiny exponents", a: `1e-1000000000`, b: `9e-999999999`, want: -1},
		{name: "tiny nonzero versus zero", a: `1e-1000000000`, b: `0`, want: 1},
		{name: "negative tiny versus zero", a: `-1e-1000000000`, b: `-0`, want: -1},
		{name: "arbitrary exponent equality", a: "1e1" + strings.Repeat("0", 80), b: "10e" + strings.Repeat("9", 80), want: 0},
		{name: "arbitrary exponent ordering", a: "1e-" + strings.Repeat("9", 80), b: `1e-1000000000`, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wire.CompareNumbers(json.Number(tt.a), json.Number(tt.b))
			if got != tt.want {
				t.Fatalf("CompareNumbers(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
			if reverse := wire.CompareNumbers(json.Number(tt.b), json.Number(tt.a)); reverse != -tt.want {
				t.Fatalf("CompareNumbers(%q, %q) = %d, want %d", tt.b, tt.a, reverse, -tt.want)
			}
		})
	}
}

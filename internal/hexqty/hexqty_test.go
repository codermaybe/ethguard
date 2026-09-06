package hexqty

import "testing"

func TestParseAcceptsEquivalentSpellings(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"0x10f", 271},
		{"0x010f", 271},                    // zero-padded spelling is equivalent
		{"0X10F", 271},                     // case-insensitive prefix and digits
		{"0x0", 0},                         // genesis height
		{"0xFFFFFFFFFFFFFFFF", ^uint64(0)}, // uint64 range edge
	}
	for _, tc := range cases {
		got, err := Parse(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("Parse(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
}

func TestParseRejectsMalformedQuantities(t *testing.T) {
	for _, in := range []string{
		"",                     // empty
		"0x",                   // prefix without digits
		"123",                  // bare decimal: malformed, not a decimal spelling
		"0x-1",                 // sign inside digits
		"-0x1",                 // sign before prefix
		"0x+1",                 // plus inside digits
		"0x 1",                 // whitespace inside digits
		"0x1 ",                 // trailing whitespace
		"0xg",                  // non-hex digit
		"x10",                  // missing prefix
		"0x1FFFFFFFFFFFFFFFFF", // beyond uint64
	} {
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", in)
		}
	}
}

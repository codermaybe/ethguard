package ethcodec

import (
	"regexp"
	"strings"
	"testing"
)

// Freeze the pre-optimization schema expressions independently of production
// constants. A shared typo in the predicate and pattern binding must not
// silently redefine the accepted language in this differential witness.
var strictASCIIReferences = []struct {
	name      string
	pattern   string
	predicate func(string) bool
	reference *regexp.Regexp
}{
	{"Quantity", QuantityPattern, IsCanonicalQuantity, regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]*)$`)},
	{"Quantity64", Quantity64Pattern, IsCanonicalQuantity64, regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]{0,15})$`)},
	{"Quantity256", Quantity256Pattern, IsCanonicalQuantity256, regexp.MustCompile(`^0x(0|[1-9a-f][0-9a-f]{0,63})$`)},
	{"Data", DataPattern, IsWholeByteData, regexp.MustCompile(`^0x([0-9a-f]{2})*$`)},
	{"Address", AddressPattern, IsAddress, regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)},
	{"HexByte", HexBytePattern, IsHexByte, regexp.MustCompile(`^0x[0-9a-f]{1,2}$`)},
	{"StorageKey", StorageKeyPattern, IsStorageKey, regexp.MustCompile(`^0x[0-9a-f]{0,64}$`)},
	{"DecimalString", DecimalStringPattern, IsCanonicalDecimal, regexp.MustCompile(`^(0|[1-9][0-9]*)$`)},
}

func checkStrictASCIIReferences(t testing.TB, text string) {
	t.Helper()
	for _, rule := range strictASCIIReferences {
		if got, want := rule.predicate(text), rule.reference.MatchString(text); got != want {
			t.Fatalf("%s(%q) = %v, frozen pattern wants %v", rule.name, text, got, want)
		}
	}
}

func strictASCIISeeds() []string {
	seeds := []string{"", "0", "00", "01", "1", "-0", "+1", "0x", "0X0", "0x0", "0x00", "0x01", "0xa", "0xA", "0x0a", "0x0A", "0xg", "0x１", "０x1", "0x\n", "1\n", " 1", "1 ", "0x\x00", "0x\xff", "\xff", "\ufeff1"}
	for _, digits := range []int{1, 2, 15, 16, 17, 39, 40, 41, 63, 64, 65, 512} {
		lower := strings.Repeat("a", digits)
		seeds = append(seeds,
			"0x"+lower, "0x"+strings.Repeat("0", digits),
			"0x"+strings.Repeat("A", digits), "0X"+lower,
			"0xg"+lower[1:], "0x"+lower[:digits-1]+"g",
			"0x"+lower+"\n", "1"+strings.Repeat("0", digits),
		)
	}
	return seeds
}

func TestStrictASCIIExactPatterns(t *testing.T) {
	for _, rule := range strictASCIIReferences {
		if rule.pattern != rule.reference.String() {
			t.Errorf("%s pattern changed: %q, want %q", rule.name, rule.pattern, rule.reference.String())
		}
	}
	for _, seed := range strictASCIISeeds() {
		checkStrictASCIIReferences(t, seed)
	}
	// Every byte is exercised inside otherwise-valid spellings, including
	// non-ASCII bytes, controls and both sides of the hex alphabet bounds.
	for value := 0; value <= 255; value++ {
		for _, digits := range []int{1, 2, 16, 40, 64} {
			for _, index := range []int{0, digits - 1} {
				text := []byte("0x" + strings.Repeat("a", digits))
				text[index+2] = byte(value)
				checkStrictASCIIReferences(t, string(text))
			}
		}
		checkStrictASCIIReferences(t, "1"+string([]byte{byte(value)}))
	}
}

func TestStrictASCIILargeBoundaries(t *testing.T) {
	for _, size := range []int{64 << 10, 1 << 20} {
		for _, text := range []string{
			"0x" + strings.Repeat("a", size),
			"0x" + strings.Repeat("a", size-1) + "g",
			"0xg" + strings.Repeat("a", size-1),
			"1" + strings.Repeat("9", size),
			"1" + strings.Repeat("9", size-1) + "a",
		} {
			checkStrictASCIIReferences(t, text)
		}
	}
}

func TestStrictASCIIValidPredicatesAllocateNothing(t *testing.T) {
	values := []string{"0xa", "0x" + strings.Repeat("a", 16), "0x" + strings.Repeat("a", 64), "0xab", "0x" + strings.Repeat("aA", 20), "0x0a", "0xabc", "123456789"}
	for i, rule := range strictASCIIReferences {
		t.Run(rule.name, func(t *testing.T) {
			value := values[i]
			if !rule.predicate(value) {
				t.Fatalf("invalid test value %q", value)
			}
			if allocations := testing.AllocsPerRun(100, func() {
				if !rule.predicate(value) {
					panic("valid scalar rejected")
				}
			}); allocations != 0 {
				t.Fatalf("allocations = %g, want zero", allocations)
			}
		})
	}
}

func FuzzStrictASCIIMatchesFrozenPatterns(f *testing.F) {
	for _, seed := range strictASCIISeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 64<<10 {
			return
		}
		checkStrictASCIIReferences(t, text)
	})
}

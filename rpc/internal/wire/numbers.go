package wire

// The numeric rules the generated native types rely on. CanonicalRatio is
// moved here from rpc/internal/objects/feehistory (the objects packages
// already depend on wire, so the rule lives beside the other canonicalizers
// and both callers share one implementation). CanonicalHexByte is the
// return-value-design.md section 5 rule: byte-like fields (type, v, yParity)
// are admitted as 1-2 hex digits and shipped as canonical QUANTITY.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CanonicalRatio reads one complete JSON number, requires its value to stay
// inside [0,1], and returns the shortest round-trip decimal. 0.50 compares
// equal to 0.5 and will be emitted as 0.5; a decimal whose shortest form would
// change its exact value is rejected. Negative zero is emitted as 0.
func CanonicalRatio(value json.RawMessage) (json.Number, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return "", fmt.Errorf("expected a decimal")
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err != nil {
		return "", fmt.Errorf("expected a decimal")
	}
	f, err := number.Float64()
	if err != nil || f < 0 || f > 1 {
		return "", fmt.Errorf("out of range")
	}
	if f == 0 {
		f = 0 // Drop the sign bit before formatting negative zero.
	}
	shortest := strconv.FormatFloat(f, 'f', -1, 64)
	if !sameDecimalValue(number.String(), shortest) {
		return "", fmt.Errorf("decimal %s is not the shortest form %s", number, shortest)
	}
	return json.Number(shortest), nil
}

// sameDecimalValue compares validated decimal spellings without expanding an
// exponent into a big integer. Even enormous exponents therefore cannot cause
// an allocation proportional to the exponent's value.
func sameDecimalValue(original, canonical string) bool {
	negative := strings.HasPrefix(original, "-")
	original = strings.TrimPrefix(original, "-")
	exponent := "0"
	if index := strings.IndexAny(original, "eE"); index >= 0 {
		exponent = original[index+1:]
		original = original[:index]
	}
	originalDigits, originalScale := decimalParts(original)
	canonicalDigits, canonicalScale := decimalParts(canonical)
	if originalDigits == "" || canonicalDigits == "" {
		return originalDigits == canonicalDigits
	}
	if negative || !sameSignificand(originalDigits, canonicalDigits) {
		return false
	}
	parsedExponent, err := strconv.Atoi(exponent)
	return err == nil && parsedExponent == originalScale-canonicalScale
}

// decimalParts trims insignificant zeros without copying the coefficient.
// The returned significand may still contain its decimal point.
func decimalParts(coefficient string) (string, int) {
	scale := 0
	if point := strings.IndexByte(coefficient, '.'); point >= 0 {
		scale = len(coefficient) - point - 1
	}
	start := 0
	for start < len(coefficient) && (coefficient[start] == '0' || coefficient[start] == '.') {
		start++
	}
	end := len(coefficient)
	for end > start {
		switch coefficient[end-1] {
		case '.':
			end--
		case '0':
			end--
			scale--
		default:
			return coefficient[start:end], scale
		}
	}
	return coefficient[start:end], scale
}

func sameSignificand(a, b string) bool {
	for i, j := 0, 0; ; {
		if i < len(a) && a[i] == '.' {
			i++
		}
		if j < len(b) && b[j] == '.' {
			j++
		}
		if i == len(a) || j == len(b) {
			return i == len(a) && j == len(b)
		}
		if a[i] != b[j] {
			return false
		}
		i++
		j++
	}
}

// CanonicalHexByte admits the official byte-shaped family (type, v, yParity):
// 0x with 1-2 hex digits. It returns the canonical QUANTITY spelling, so
// 0x4, 0x04, and the lowercase form of 0x4A all ship as the same canonical
// bytes.
func CanonicalHexByte(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("hex byte: expected a string")
	}
	if !strings.HasPrefix(text, "0x") {
		return "", fmt.Errorf("hex byte: expected 0x plus 1-2 hex digits")
	}
	digits := text[2:]
	if len(digits) == 0 || len(digits) > 2 {
		return "", fmt.Errorf("hex byte: expected 0x plus 1-2 hex digits")
	}
	var parsed byte
	for i := range digits {
		var digit byte
		switch c := digits[i]; {
		case c >= '0' && c <= '9':
			digit = c - '0'
		case c >= 'a' && c <= 'f':
			digit = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			digit = c - 'A' + 10
		default:
			return "", fmt.Errorf("hex byte: malformed value")
		}
		parsed = parsed<<4 | digit
	}
	const hexDigits = "0123456789abcdef"
	canonical := [4]byte{'0', 'x', hexDigits[parsed>>4], hexDigits[parsed&0xf]}
	if parsed < 16 {
		canonical[2] = canonical[3]
		return string(canonical[:3]), nil
	}
	return string(canonical[:]), nil
}

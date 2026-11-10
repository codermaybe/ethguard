package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// CanonicalNumber validates one complete JSON number and preserves its exact
// decimal value, without a floating-point conversion or schema-specific bounds.
// Insignificant zeros and negative zero are removed. Scientific exponents from
// -6 through 20 use fixed notation; all other exponents use scientific notation.
// This formatting policy bounds zero padding, not the accepted numeric domain.
func CanonicalNumber(raw json.RawMessage) (json.Number, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return "", fmt.Errorf("expected a JSON number")
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", fmt.Errorf("expected a JSON number: %w", err)
	}
	var parsed exactNumber
	parseExactNumber(number.String(), &parsed)
	if parsed.significand == "" {
		return "0", nil
	}

	var result strings.Builder
	fixed := parsed.order.IsInt64() && parsed.order.Int64() >= -6 && parsed.order.Int64() <= 20
	if fixed {
		result.Grow(parsed.digitCount + 24)
		if parsed.negative {
			result.WriteByte('-')
		}
		point := int(parsed.order.Int64()) + 1
		if point <= 0 {
			result.WriteString("0.")
			for range -point {
				result.WriteByte('0')
			}
			writeExactSignificand(&result, parsed.significand, -1)
		} else {
			writeExactSignificand(&result, parsed.significand, point)
			for i := parsed.digitCount; i < point; i++ {
				result.WriteByte('0')
			}
		}
	} else {
		exponent := parsed.order.String()
		result.Grow(parsed.digitCount + len(exponent) + 3)
		if parsed.negative {
			result.WriteByte('-')
		}
		writeExactSignificand(&result, parsed.significand, 1)
		result.WriteByte('e')
		result.WriteString(exponent)
	}
	return json.Number(result.String()), nil
}

// CompareNumbers compares the exact decimal values of two validated JSON
// numbers. It returns -1, 0, or 1, like strings.Compare. Neither operand needs
// to be canonical. Exponents are compared without expanding powers of ten.
func CompareNumbers(a, b json.Number) int {
	var left, right exactNumber
	parseExactNumber(a.String(), &left)
	parseExactNumber(b.String(), &right)
	if left.significand == "" {
		if right.significand == "" {
			return 0
		}
		if right.negative {
			return 1
		}
		return -1
	}
	if right.significand == "" {
		if left.negative {
			return -1
		}
		return 1
	}
	if left.negative != right.negative {
		if left.negative {
			return -1
		}
		return 1
	}
	comparison := left.order.Cmp(&right.order)
	if comparison == 0 {
		comparison = compareExactSignificands(left.significand, right.significand)
	}
	if left.negative {
		return -comparison
	}
	return comparison
}

// exactNumber stores the nonzero significand as a view of the input, possibly
// still containing its decimal point. order is the scientific exponent, not a
// power of ten: its storage grows with the input exponent's digit count, never
// with the exponent's numeric value. An empty significand represents zero.
// Values are filled in place and their big.Int fields are not shallow-copied.
type exactNumber struct {
	negative    bool
	significand string
	digitCount  int
	order       big.Int
}

// parseExactNumber requires validated JSON syntax and a fresh result.
func parseExactNumber(number string, result *exactNumber) {
	if number[0] == '-' {
		result.negative = true
		number = number[1:]
	}
	coefficient := number
	exponent := ""
	if index := strings.IndexAny(number, "eE"); index >= 0 {
		coefficient = number[:index]
		exponent = number[index+1:]
	}
	var scale int
	result.significand, scale = decimalParts(coefficient)
	if result.significand == "" {
		result.negative = false
		return
	}
	result.digitCount = len(result.significand)
	if strings.IndexByte(result.significand, '.') >= 0 {
		result.digitCount--
	}
	if exponent != "" {
		result.order.SetString(exponent, 10)
	}
	var adjustment big.Int
	adjustment.SetInt64(int64(result.digitCount) - 1 - int64(scale))
	result.order.Add(&result.order, &adjustment)
}

// writeExactSignificand omits the source decimal point and, when needed,
// inserts the output decimal point after the requested number of digits.
func writeExactSignificand(result *strings.Builder, significand string, point int) {
	written := 0
	for i := range significand {
		if significand[i] == '.' {
			continue
		}
		if written == point {
			result.WriteByte('.')
		}
		result.WriteByte(significand[i])
		written++
	}
}

func compareExactSignificands(a, b string) int {
	for i, j := 0, 0; i < len(a) || j < len(b); {
		if i < len(a) && a[i] == '.' {
			i++
		}
		if j < len(b) && b[j] == '.' {
			j++
		}
		left, right := byte('0'), byte('0')
		if i < len(a) {
			left = a[i]
			i++
		}
		if j < len(b) {
			right = b[j]
			j++
		}
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
	}
	return 0
}

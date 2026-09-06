// Package hexqty holds the one shared wire-quantity parsing primitive:
// turning a 0x-prefixed hexadecimal quantity string into a uint64. It is
// internal so the spelling contract stays an implementation detail; wire
// semantics — which response fields are quantities at all — remain with the
// method policy layer. Comparison normalizers that must admit arbitrary
// length stay in rpc/methods; this package serves only height-shaped values
// that must fit a uint64.
package hexqty

import (
	"fmt"
	"strconv"
)

// Parse parses one 0x-prefixed hexadecimal quantity. The prefix is
// mandatory: a bare decimal string is malformed, not a decimal spelling —
// parsing "123" as hex would silently misread it. Digits are
// case-insensitive, signs and whitespace are rejected, and values beyond
// the uint64 range are rejected rather than truncated.
func Parse(value string) (uint64, error) {
	if len(value) < 3 || (value[:2] != "0x" && value[:2] != "0X") {
		return 0, fmt.Errorf("hex quantity: invalid quantity %q", value)
	}
	parsed, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("hex quantity: invalid quantity %q", value)
	}
	return parsed, nil
}

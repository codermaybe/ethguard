package rpc

import (
	"fmt"
	"strconv"
)

// ParseHexQuantity parses one 0x-prefixed hexadecimal quantity string into uint64.
// The prefix is mandatory: a bare decimal string is malformed. Digits are
// case-insensitive, signs and whitespace are rejected, and values beyond uint64
// range are rejected rather than truncated.
func ParseHexQuantity(value string) (uint64, error) {
	if len(value) < 3 || (value[:2] != "0x" && value[:2] != "0X") {
		return 0, fmt.Errorf("hex quantity: invalid quantity %q", value)
	}
	parsed, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("hex quantity: invalid quantity %q", value)
	}
	return parsed, nil
}

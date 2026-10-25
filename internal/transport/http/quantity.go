package http

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// decodeHexQuantity decodes a canonical hex quantity (EIP-1474 / hexutil
// encoding) into a big.Int. The decoder is project-owned: go-ethereum's
// hexutil is not used, so the native adapter stays free of go-ethereum
// imports.
//
// Canonical form: a JSON string, "0x" prefix, at least one digit, and no
// leading zeros — except the single zero value "0x0". Mixed-case hex digits
// are accepted, as in the hexutil encoding this project's wire forms follow.
func decodeHexQuantity(raw json.RawMessage) (*big.Int, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("http: quantity must be a JSON string, got %s", raw)
	}
	digits, ok := strings.CutPrefix(s, "0x")
	if !ok || digits == "" {
		return nil, fmt.Errorf("http: quantity %q is not a 0x-prefixed hex string", s)
	}
	if len(digits) > 1 && digits[0] == '0' {
		return nil, fmt.Errorf("http: quantity %q is not canonical: leading zeros", s)
	}
	for i := 0; i < len(digits); i++ {
		if !isHexDigit(digits[i]) {
			return nil, fmt.Errorf("http: quantity %q contains non-hex digit %q", s, digits[i])
		}
	}
	v, ok := new(big.Int).SetString(digits, 16)
	if !ok {
		return nil, fmt.Errorf("http: quantity %q does not parse as hex", s)
	}
	return v, nil
}

func isHexDigit(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'f':
		return true
	case c >= 'A' && c <= 'F':
		return true
	}
	return false
}

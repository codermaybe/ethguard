package ethcodec

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Compatibility identifiers describe finite transformations, not a second
// schema validator. Their result must enter the strict constructor afterwards.
const (
	CompatWireHexCase        = "wire-hex-case-v1"
	CompatLowerPrefixHexCase = "lower-prefix-hex-case-v1"
	CompatPeerQuantityZeros  = "peer-quantity-leading-zeros-v1"
	CompatLegacyDecimal      = "legacy-decimal-nonnegative-v1"
)

func decodedScalarString(raw json.RawMessage, kind string) (string, error) {
	value, err := DecodeJSONString(raw)
	if err != nil {
		if kind != "" {
			kind += ": "
		}
		return "", fmt.Errorf("%sexpected string: %w", kind, err)
	}
	return value, nil
}

// FoldWireHexCase preserves the legacy allowance for 0X and uppercase digits.
func FoldWireHexCase(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodedScalarString(raw, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(foldWireHexText(value))
}
func foldWireHexText(value string) string {
	if len(value) >= 2 && strings.EqualFold(value[:2], "0x") {
		return strings.ToLower(value)
	}
	return value
}

// FoldLowerPrefixHexCase preserves the literal lowercase prefix requirement
// of code, successful call data and storage results.
func FoldLowerPrefixHexCase(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodedScalarString(raw, "")
	if err != nil {
		return nil, err
	}
	value, err = foldLowerPrefixHexText(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func foldLowerPrefixHexText(value string) (string, error) {
	if len(value) < 2 || value[:2] != "0x" {
		return "", fmt.Errorf("expected lowercase 0x prefix")
	}
	return strings.ToLower(value), nil
}

// FoldPeerQuantity also folds redundant leading zeroes. It does not permit 0X.
func FoldPeerQuantity(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodedScalarString(raw, "")
	if err != nil {
		return nil, err
	}
	value, err = foldPeerQuantityText(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func foldPeerQuantityText(value string) (string, error) {
	value, err := foldLowerPrefixHexText(value)
	if err != nil {
		return "", err
	}
	if len(value) == 2 {
		return value, nil
	}
	digits := strings.TrimLeft(value[2:], "0")
	if digits == "" {
		digits = "0"
	}
	return "0x" + digits, nil
}

// FoldLegacyDecimal preserves +, redundant zeroes and negative zero. Negative
// nonzero values remain negative and are rejected by the strict decimal gate.
func FoldLegacyDecimal(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodedScalarString(raw, "decimal string")
	if err != nil {
		return nil, err
	}
	value, err = foldLegacyDecimalText(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func foldLegacyDecimalText(value string) (string, error) {
	number, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return "", fmt.Errorf("decimal string: invalid number %q", value)
	}
	return number.String(), nil
}

// QuantityDecimal changes representation only after a strict quantity check.
func QuantityDecimal(text string) (string, error) {
	if !IsCanonicalQuantity(text) {
		return "", fmt.Errorf("hex quantity: invalid canonical quantity %q", text)
	}
	number, _ := new(big.Int).SetString(text[2:], 16)
	return number.String(), nil
}

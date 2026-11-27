package ethcodec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeWireQuantity applies named case compatibility, then the same
// strict scalar primitive used by generated QUANTITY constructors.
func NormalizeWireQuantity(value json.RawMessage) (string, error) {
	text, err := decodedScalarString(value, "hex quantity")
	if err != nil {
		return "", err
	}
	return QuantityDecimal(foldWireHexText(text))
}

func NormalizeWireData(value json.RawMessage) (string, error) {
	text, err := decodedScalarString(value, "hex data")
	if err != nil {
		return "", err
	}
	text = foldWireHexText(text)
	if !IsWholeByteData(text) {
		return "", fmt.Errorf("hex data: invalid data %q", text)
	}
	return text, nil
}

func ValidateWireFixedHex(value json.RawMessage, digits int, kind string) (string, error) {
	text, err := decodedScalarString(value, kind)
	if err != nil {
		return "", err
	}
	text = foldWireHexText(text)
	if !IsLowerFixedHex(text, digits) {
		return "", fmt.Errorf("%s: expected 0x-prefixed value with %d hex digits", kind, digits)
	}
	return text, nil
}

// NormalizeHexQuantity retains peer-count numeric compatibility before the
// strict quantity gate; no other method gains this leading-zero allowance.
func NormalizeHexQuantity(value json.RawMessage) (string, error) {
	text, err := decodedScalarString(value, "hex quantity")
	if err != nil {
		return "", err
	}
	text, err = foldPeerQuantityText(text)
	if err != nil {
		return "", fmt.Errorf("hex quantity: %w", err)
	}
	return QuantityDecimal(text)
}

func NormalizeHexData(value json.RawMessage) (string, error) {
	text, err := decodedScalarString(value, "hex data")
	if err != nil {
		return "", err
	}
	text, err = foldLowerPrefixHexText(text)
	if err != nil {
		return "", fmt.Errorf("hex data: %w", err)
	}
	if !IsWholeByteData(text) {
		return "", fmt.Errorf("hex data: invalid data %q", text)
	}
	return text, nil
}

// The 32-byte storage length is an additional method constraint on DATA.
func NormalizeStorageWord(value json.RawMessage) (string, error) {
	text, err := NormalizeHexData(value)
	if err != nil {
		return "", err
	}
	if len(text) != 66 {
		return "", fmt.Errorf("storage word: expected 32-byte 0x-prefixed data, got %q", text)
	}
	return text, nil
}

func NormalizeDecimalString(value json.RawMessage) (string, error) {
	text, err := decodedScalarString(value, "decimal string")
	if err != nil {
		return "", err
	}
	text, err = foldLegacyDecimalText(text)
	if err != nil {
		return "", err
	}
	if !IsCanonicalDecimal(text) {
		return "", fmt.Errorf("decimal string: expected nonnegative number")
	}
	return text, nil
}

// canonicalizeHex lowercases hexadecimal casing in decoded JSON so key order
// and casing differences do not cause false conflicts. It is the shared
// primitive behind the pruning engine's comparison keys (PruneMethod).
func CanonicalizeHex(value *any) {
	switch item := (*value).(type) {
	case map[string]any:
		for key, child := range item {
			CanonicalizeHex(&child)
			item[key] = child
		}
	case []any:
		for i := range item {
			CanonicalizeHex(&item[i])
		}
	case string:
		if len(item) >= 2 && strings.EqualFold(item[:2], "0x") {
			*value = "0x" + strings.ToLower(item[2:])
		}
	}
}

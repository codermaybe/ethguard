package wire

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// normalizeWireQuantity validates the canonical JSON-RPC QUANTITY grammar
// before converting it to a comparison value. It is intentionally separate
// from normalizeHexQuantity, whose historical numeric-equivalence behavior is
// retained until each method opts into the stricter wire contract.
func NormalizeWireQuantity(value json.RawMessage) (string, error) {
	var quantity string
	if err := json.Unmarshal(value, &quantity); err != nil {
		return "", fmt.Errorf("hex quantity: expected string: %w", err)
	}
	if len(quantity) < 3 || !strings.EqualFold(quantity[:2], "0x") {
		return "", fmt.Errorf("hex quantity: invalid quantity %q", quantity)
	}
	digits := quantity[2:]
	if digits != "0" && strings.HasPrefix(digits, "0") {
		return "", fmt.Errorf("hex quantity: non-canonical leading zero in %q", quantity)
	}
	for _, digit := range digits {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') || (digit >= 'A' && digit <= 'F')) {
			return "", fmt.Errorf("hex quantity: invalid quantity %q", quantity)
		}
	}
	number, ok := new(big.Int).SetString(digits, 16)
	if !ok {
		return "", fmt.Errorf("hex quantity: invalid quantity %q", quantity)
	}
	return number.String(), nil
}

// normalizeWireData validates arbitrary DATA while preserving byte length
// and leading zeroes. Case differences are the only normalization applied.
func NormalizeWireData(value json.RawMessage) (string, error) {
	var data string
	if err := json.Unmarshal(value, &data); err != nil {
		return "", fmt.Errorf("hex data: expected string: %w", err)
	}
	if len(data) < 2 || !strings.EqualFold(data[:2], "0x") || len(data[2:])%2 != 0 {
		return "", fmt.Errorf("hex data: invalid data %q", data)
	}
	if _, err := hex.DecodeString(data[2:]); err != nil {
		return "", fmt.Errorf("hex data: invalid data %q: %w", data, err)
	}
	return "0x" + strings.ToLower(data[2:]), nil
}

func ValidateWireFixedHex(value json.RawMessage, digits int, kind string) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("%s: expected string: %w", kind, err)
	}
	if len(text) != digits+2 || !strings.EqualFold(text[:2], "0x") {
		return "", fmt.Errorf("%s: expected 0x-prefixed value with %d hex digits", kind, digits)
	}
	if _, err := hex.DecodeString(text[2:]); err != nil {
		return "", fmt.Errorf("%s: invalid hex: %w", kind, err)
	}
	return "0x" + strings.ToLower(text[2:]), nil
}

// normalizeHexQuantity compares hex quantities numerically, accepting
// equivalent spellings rather than comparing JSON bytes. It is the shared
// normalizer for every quantity-shaped result (chain ID, balances, and
// future quantity-class methods).
func NormalizeHexQuantity(value json.RawMessage) (string, error) {
	var quantity string
	if err := json.Unmarshal(value, &quantity); err != nil {
		return "", fmt.Errorf("hex quantity: expected string: %w", err)
	}
	if len(quantity) < 3 || quantity[:2] != "0x" {
		return "", fmt.Errorf("hex quantity: invalid quantity %q", quantity)
	}
	digits := quantity[2:]
	if len(digits)%2 != 0 {
		digits = "0" + digits
	}
	bytes, err := hex.DecodeString(digits)
	if err != nil {
		return "", fmt.Errorf("hex quantity: invalid quantity %q: %w", quantity, err)
	}
	return new(big.Int).SetBytes(bytes).String(), nil
}

// normalizeHexData compares hex data strings (bytecode, storage words)
// case-insensitively. Unlike quantities, data is arbitrary-length and may be
// empty ("0x" is valid data — the code of an externally owned account), so
// numeric re-encoding would reject honest answers.
func NormalizeHexData(value json.RawMessage) (string, error) {
	var data string
	if err := json.Unmarshal(value, &data); err != nil {
		return "", fmt.Errorf("hex data: expected string: %w", err)
	}
	if len(data) < 2 || data[:2] != "0x" {
		return "", fmt.Errorf("hex data: invalid data %q", data)
	}
	// JSON-RPC DATA values are byte strings.  Requiring an even number of
	// hexadecimal digits keeps malformed provider responses out of a voting
	// cluster; hexutil.Bytes applies the same wire contract at the typed
	// adapter boundary.  The empty byte string (0x) remains valid for code
	// reads from EOAs and for successful eth_call results.
	if len(data[2:])%2 != 0 {
		return "", fmt.Errorf("hex data: odd number of digits in %q", data)
	}
	if _, err := hex.DecodeString(data[2:]); err != nil {
		return "", fmt.Errorf("hex data: invalid data %q: %w", data, err)
	}
	return strings.ToLower(data), nil
}

// normalizeStorageWord applies the stricter result shape of
// eth_getStorageAt.  A storage slot is always exactly one 32-byte word;
// unlike arbitrary DATA (bytecode, eth_call return data), short or long
// strings are malformed responses rather than alternate encodings.
func NormalizeStorageWord(value json.RawMessage) (string, error) {
	var data string
	if err := json.Unmarshal(value, &data); err != nil {
		return "", fmt.Errorf("storage word: expected string: %w", err)
	}
	if len(data) != 66 || data[:2] != "0x" {
		return "", fmt.Errorf("storage word: expected 32-byte 0x-prefixed data, got %q", data)
	}
	if _, err := hex.DecodeString(data[2:]); err != nil {
		return "", fmt.Errorf("storage word: invalid data %q: %w", data, err)
	}
	return strings.ToLower(data), nil
}

// normalizeDecimalString compares decimal strings numerically, accepting
// equivalent spellings. net_version spells the chain identity in decimal —
// a separate encoder from eth_chainId's hex quantity, hence a separate
// normalizer rather than a shared one.
func NormalizeDecimalString(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("decimal string: expected string: %w", err)
	}
	number, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return "", fmt.Errorf("decimal string: invalid number %q", text)
	}
	return number.String(), nil
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

package methods

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// normalizeHexQuantity compares hex quantities numerically, accepting
// equivalent spellings rather than comparing JSON bytes. It is the shared
// normalizer for every quantity-shaped result (chain ID, balances, and
// future quantity-class methods).
func normalizeHexQuantity(value json.RawMessage) (string, error) {
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
func normalizeHexData(value json.RawMessage) (string, error) {
	var data string
	if err := json.Unmarshal(value, &data); err != nil {
		return "", fmt.Errorf("hex data: expected string: %w", err)
	}
	if len(data) < 2 || data[:2] != "0x" {
		return "", fmt.Errorf("hex data: invalid data %q", data)
	}
	return strings.ToLower(data), nil
}

// normalizeDecimalString compares decimal strings numerically, accepting
// equivalent spellings. net_version spells the chain identity in decimal —
// a separate encoder from eth_chainId's hex quantity, hence a separate
// normalizer rather than a shared one.
func normalizeDecimalString(value json.RawMessage) (string, error) {
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

// normalizeCanonicalObject compares whole JSON objects after canonicalizing
// hexadecimal casing; JSON object key order does not cause false conflicts.
// It is the shared normalizer for immutable-content object reads (blocks,
// receipts): the payload is fully determined by the request's hash argument,
// so every benign encoding difference is erased here and whatever still
// differs between sources is dissent, not noise.
func normalizeCanonicalObject(value json.RawMessage) (string, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	canonicalizeHex(&decoded)
	encoded, err := json.Marshal(decoded)
	return string(encoded), err
}

func canonicalizeHex(value *any) {
	switch item := (*value).(type) {
	case map[string]any:
		for key, child := range item {
			canonicalizeHex(&child)
			item[key] = child
		}
	case []any:
		for i := range item {
			canonicalizeHex(&item[i])
		}
	case string:
		if strings.HasPrefix(item, "0x") {
			*value = strings.ToLower(item)
		}
	}
}

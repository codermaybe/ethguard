package methods

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
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

// Package methods holds one policy per verified Ethereum JSON-RPC method.
//
// The parent rpc package owns the Policy contract, the Registry, and the
// shared resolution strategies — everything true of any method and any JSON.
// This package owns Ethereum method semantics: prepare, normalize, compare,
// anchor, and classify each supported method. Unknown methods never get a
// file here; they fall through the registry to transparent forwarding, which
// must produce Forwarded, never Verified.
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
func normalizeStorageWord(value json.RawMessage) (string, error) {
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

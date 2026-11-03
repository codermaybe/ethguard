package methods

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// NormalizeBlockHead exposes the block identity contract to the reference
// head codec without constructing a legacy policy chain. The method resolver
// and the reference establishment seam therefore share the same business
// normalizer while keeping their assembly ownership separate.
func NormalizeBlockHead(value json.RawMessage) (string, error) {
	return normalizeBlockIdentity(value)
}

// normalizeBlockIdentity reduces a block object to its comparable identity.
// Extra fields, including vendor extensions, do not change the key and are
// not rejected here. Missing number or hash has no identity, so it errors
// instead of voting. Body checks belong to Admit.
//
// normalizeBlockHead reduces a block object to its comparable identity:
// "<decimal number>|<lowercase hash>". Parsing the height numerically makes
// equivalent hexadecimal spellings (for example 0x010f and 0x10f) agree.
// The height is a uint64-shaped quantity, so the shared wire-quantity
// primitive owns the spelling contract. Number and hash are the identity
// the key is defined on; other body fields are not checked here.
func normalizeBlockIdentity(value json.RawMessage) (string, error) {
	if string(value) == "null" {
		return "null", nil
	}
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(value, &head); err != nil {
		return "", err
	}
	if head.Number == "" || len(head.Hash) < 3 {
		return "", fmt.Errorf("block head: missing number or hash")
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	if err != nil {
		return "", fmt.Errorf("block head: %w", err)
	}
	return strconv.FormatUint(height, 10) + "|" + strings.ToLower(head.Hash), nil
}

package methods

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"ethguard/internal/hexqty"
	"ethguard/rpc"
)

// MethodGetBlockByNumber is the JSON-RPC method name resolved by
// BlockByNumberPolicy.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// BlockByNumberPolicy verifies eth_getBlockByNumber by full agreement on the
// block head: number and hash must match across sources, hexadecimal casing
// and key order aside. It also serves as the anchor policy for pinned reads.
func BlockByNumberPolicy(opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Strict(normalizeBlockHead, opts...)
}

// normalizeBlockHead reduces a block object to its comparable identity:
// "<decimal number>|<lowercase hash>". Parsing the height numerically makes
// equivalent hexadecimal spellings (for example 0x010f and 0x10f) agree.
// The height is a uint64-shaped quantity, so the shared wire-quantity
// primitive owns the spelling contract; the required-field checks here are
// this method's wire semantics.
func normalizeBlockHead(value json.RawMessage) (string, error) {
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
	height, err := hexqty.Parse(head.Number)
	if err != nil {
		return "", fmt.Errorf("block head: %w", err)
	}
	return strconv.FormatUint(height, 10) + "|" + strings.ToLower(head.Hash), nil
}

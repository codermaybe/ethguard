package methods

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

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
	if len(head.Number) < 3 || !strings.EqualFold(head.Number[:2], "0x") {
		return "", fmt.Errorf("block head: invalid number %q", head.Number)
	}
	height, ok := new(big.Int).SetString(head.Number[2:], 16)
	if !ok {
		return "", fmt.Errorf("block head: invalid number %q", head.Number)
	}
	return height.String() + "|" + strings.ToLower(head.Hash), nil
}

package methods

import (
	"encoding/json"
	"fmt"
	"strings"

	"ethguard/rpc"
)

// MethodGetBlockByNumber is the JSON-RPC method name resolved by
// BlockByNumberPolicy.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// BlockByNumberPolicy verifies eth_getBlockByNumber by full agreement on the
// block head: number and hash must match across sources, hexadecimal casing
// and key order aside. It also serves as the anchor policy for pinned reads.
func BlockByNumberPolicy() rpc.Policy { return rpc.Strict(normalizeBlockHead) }

// normalizeBlockHead reduces a block object to its comparable identity:
// "<number>|<hash>", both lowercased.
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
	return strings.ToLower(head.Number) + "|" + strings.ToLower(head.Hash), nil
}

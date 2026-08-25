package methods

import (
	"encoding/json"
	"fmt"

	"ethguard/rpc"
)

// MethodGetBlockByNumber is the JSON-RPC method name resolved by
// BlockByNumberPolicy.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// BlockByNumberPolicy verifies eth_getBlockByNumber by full agreement on the
// block head: number and hash must match across sources, hexadecimal casing
// and key order aside. It also serves as the anchor policy for pinned reads.
func BlockByNumberPolicy() rpc.Policy { return rpc.Strict(NormalizeBlockHead) }

// NormalizeBlockHead reduces a block object to its comparable identity:
// "<number>|<hash>" with both lowercased.
func NormalizeBlockHead(value json.RawMessage) (string, error) {
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
	return lower(head.Number) + "|" + lower(head.Hash), nil
}

func lower(s string) string {
	out := []byte(s)
	for i := range out {
		if 'A' <= out[i] && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}

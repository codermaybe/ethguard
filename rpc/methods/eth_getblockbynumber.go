package methods

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
)

// MethodGetBlockByNumber is the JSON-RPC method name resolved by
// BlockByNumberPolicy.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// BlockByNumberPolicy corroborates eth_getBlockByNumber by full agreement on the
// block head: number and hash must match across sources, hexadecimal casing
// and key order aside. It also serves as the anchor policy for pinned reads.
// The pending tag resolves as a node-local observation (decision 0035), never
// Corroborated through agreement and never rewritten into an anchored read.
// Anchor establishment never uses pending. The typed adapter rejects
// BlockByNumber(-1) before fan-out; this policy does not project that tag
// into an ordinary block.
func BlockByNumberPolicy(opts ...rpc.StrictOption) rpc.Policy {
	strictOpts := append([]rpc.StrictOption{rpc.WithCanonicalObject(blockobject.EncodeByNumber)}, opts...)
	return PendingLocal(0)(blockPolicy{Policy: rpc.Strict(normalizeBlockIdentity, compareOpts(rpc.RuleCompareMethodSpecific, strictOpts)...), byHash: false})
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

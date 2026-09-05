package methods

import (
	"ethguard/rpc"
)

// MethodGetTransactionCount is the JSON-RPC method name resolved by
// GetTransactionCountPolicy.
const MethodGetTransactionCount = "eth_getTransactionCount"

// GetTransactionCountPolicy verifies eth_getTransactionCount at an agreed
// block reference — the account nonce is state that moves with the head, so
// moving tags are rewritten before fan-out (see GetBalancePolicy). Strict
// options pass through to the underlying strict resolution.
func GetTransactionCountPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Anchored(1, normalizeHexQuantity, anchor, opts...)
}

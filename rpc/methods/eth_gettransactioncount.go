package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodGetTransactionCount is the JSON-RPC method name resolved by
// GetTransactionCountPolicy.
const MethodGetTransactionCount = "eth_getTransactionCount"

// GetTransactionCountPolicy corroborates eth_getTransactionCount at an agreed
// block reference — the account nonce is state that moves with the head, so
// moving tags are rewritten before fan-out (see GetBalancePolicy). Strict
// options pass through to the underlying strict resolution. Explicit block
// numbers, hashes, and pending pass through unchanged.
func GetTransactionCountPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Anchored(1, wire.NormalizeWireQuantity, anchor, opts...)
}

package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetTransactionCount is the JSON-RPC method name resolved by
// GetTransactionCountPolicy.
const MethodGetTransactionCount = "eth_getTransactionCount"

// GetTransactionCountPolicy corroborates eth_getTransactionCount at an agreed
// block reference — the account nonce is state that moves with the head, so
// moving tags are rewritten before fan-out (see GetBalancePolicy). Strict
// options pass through to the underlying strict resolution. Explicit block
// numbers, hashes, and pending pass through unchanged. EIP-1898 object forms
// ride EIP1898BlockRef (see GetBalancePolicy); quantity-only positions never do.
// Pending resolves as a node-local observation (see GetBalancePolicy).
func GetTransactionCountPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	opts = append([]rpc.StrictOption{rpc.WithCanonicalValue(wire.EncodeQuantity)}, opts...)
	return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.Anchored(1, wire.NormalizeWireQuantity, anchor, compareOpts(rpc.RuleCompareQuantity, opts)...)))
}

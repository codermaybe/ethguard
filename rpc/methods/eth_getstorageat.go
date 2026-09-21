package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodGetStorageAt is the JSON-RPC method name resolved by
// GetStorageAtPolicy.
const MethodGetStorageAt = "eth_getStorageAt"

// GetStorageAtPolicy corroborates eth_getStorageAt at an agreed block reference.
// The answer is exactly one 32-byte data word, not a quantity: its
// method-specific normalizer rejects null, missing, short, long, and invalid
// hex responses. The storage key (position 1) passes through untouched; the
// block tag (position 2) is rewritten for latest/safe/finalized while
// explicit numbers, hashes, and pending pass through. EIP-1898 object forms
// ride EIP1898BlockRef (see GetBalancePolicy). Pending resolves as a
// node-local observation (see GetBalancePolicy). Strict options pass
// through to the underlying strict resolution.
func GetStorageAtPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return PendingLocal(2)(EIP1898BlockRef(anchor, 2)(rpc.Anchored(2, wire.NormalizeStorageWord, anchor, opts...)))
}

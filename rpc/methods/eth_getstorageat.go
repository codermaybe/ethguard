package methods

import (
	"ethguard/rpc"
)

// MethodGetStorageAt is the JSON-RPC method name resolved by
// GetStorageAtPolicy.
const MethodGetStorageAt = "eth_getStorageAt"

// GetStorageAtPolicy verifies eth_getStorageAt at an agreed block reference.
// The answer is exactly one 32-byte data word, not a quantity: its
// method-specific normalizer rejects null, missing, short, long, and invalid
// hex responses. The storage key (position 1) passes through untouched; the
// block tag (position 2) is rewritten for latest/safe/finalized while
// explicit numbers, hashes, and pending pass through. Strict options pass
// through to the underlying strict resolution.
func GetStorageAtPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Anchored(2, normalizeStorageWord, anchor, opts...)
}

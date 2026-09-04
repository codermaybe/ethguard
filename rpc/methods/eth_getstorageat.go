package methods

import (
	"ethguard/rpc"
)

// MethodGetStorageAt is the JSON-RPC method name resolved by
// GetStorageAtPolicy.
const MethodGetStorageAt = "eth_getStorageAt"

// GetStorageAtPolicy verifies eth_getStorageAt at an agreed block reference.
// The answer is a 32-byte data word, not a quantity: it rides the hex-data
// normalizer. The storage key (position 1) passes through untouched; the
// block tag (position 2) is rewritten.
func GetStorageAtPolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return rpc.Anchored(2, normalizeHexData, anchor)
}

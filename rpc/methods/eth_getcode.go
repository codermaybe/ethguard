package methods

import (
	"ethguard/rpc"
)

// MethodGetCode is the JSON-RPC method name resolved by GetCodePolicy.
const MethodGetCode = "eth_getCode"

// GetCodePolicy verifies eth_getCode at an agreed block reference. The
// answer is arbitrary-length bytecode — hex data, not a quantity; an
// externally owned account answers "0x", which is valid data and would
// fail a quantity-shaped normalizer.
func GetCodePolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return rpc.Anchored(1, normalizeHexData, anchor)
}

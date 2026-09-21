package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodGetCode is the JSON-RPC method name resolved by GetCodePolicy.
const MethodGetCode = "eth_getCode"

// GetCodePolicy corroborates eth_getCode at an agreed block reference. The
// answer is arbitrary-length bytecode — hex data, not a quantity; an
// externally owned account answers "0x", which is valid data and would
// fail a quantity-shaped normalizer. Strict options pass through to the
// underlying strict resolution. Moving tags are anchored; explicit block
// numbers, hashes, and pending pass through unchanged.
func GetCodePolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Anchored(1, wire.NormalizeHexData, anchor, opts...)
}

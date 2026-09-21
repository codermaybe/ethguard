package methods

import (
	"encoding/json"

	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodCreateAccessList is the JSON-RPC method name resolved by
// CreateAccessListPolicy.
const MethodCreateAccessList = "eth_createAccessList"

// CreateAccessListPolicy corroborates EIP-2930 access list creation over an agreed
// block reference. The block tag at position 1 (if specified) is rewritten to
// the corroborated block reference before fan-out so sources evaluate identical state.
// Responses are compared with canonical object normalization so hexadecimal casing
// or JSON key ordering differences do not cause false conflicts.
func CreateAccessListPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Anchored(1, normalizeAccessList, anchor, opts...)
}

// normalizeAccessList reduces one access-list answer to its pruned comparison
// key: hexadecimal casing or JSON key ordering differences do not cause false
// conflicts, and vendor extension fields do not either.
func normalizeAccessList(value json.RawMessage) (string, error) {
	return wire.PruneMethod(MethodCreateAccessList, value)
}

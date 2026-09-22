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
// Position 1 accepts the same EIP-1898 object shapes as the T02 state reads:
// moving-tag objects rewrite to {blockHash[, requireCanonical]}, explicit
// objects pass through, and malformed objects fail locally before any anchor
// call. Pending, including {blockNumber: pending}, resolves as a node-local
// observation (decision 0035), never Corroborated through agreement.
func CreateAccessListPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.Anchored(1, normalizeAccessList, anchor, opts...)))
}

// normalizeAccessList reduces one access-list answer to its pruned comparison
// key: hexadecimal casing or JSON key ordering differences do not cause false
// conflicts, and vendor extension fields do not either.
func normalizeAccessList(value json.RawMessage) (string, error) {
	return wire.PruneMethod(MethodCreateAccessList, value)
}

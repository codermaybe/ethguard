package methods

import (
	"ethguard/rpc"
)

// MethodGetBlockReceipts is the JSON-RPC method name resolved by
// BlockReceiptsPolicy.
const MethodGetBlockReceipts = "eth_getBlockReceipts"

// BlockReceiptsPolicy verifies eth_getBlockReceipts by full agreement over
// the whole receipts array. A block hash determines its receipts fully, so
// the payload is immutable content; hash and height parameters pass through
// untouched, while moving tags are rewritten to the verified reference —
// the same split every anchored read uses (see rpc.Anchored).
func BlockReceiptsPolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return rpc.Anchored(0, normalizeCanonicalObject, anchor)
}

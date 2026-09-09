package methods

import (
	"ethguard/rpc"
)

// MethodGetBlockReceipts is the JSON-RPC method name resolved by
// BlockReceiptsPolicy.
const MethodGetBlockReceipts = "eth_getBlockReceipts"

// BlockReceiptsPolicy verifies eth_getBlockReceipts by full agreement over
// the whole receipts array. A block hash determines its receipts fully, so
// the payload is immutable content. Hashes and explicit numbers pass through;
// moving tags are rewritten to the verified reference. pending is rejected in
// Prepare because this method has no stable pending-block receipt semantics.
func BlockReceiptsPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	prepared := rpc.RejectPending(0)(rpc.WithAnchor(anchor, 0)(rpc.Strict(normalizeBlockReceiptList, opts...)))
	return admissionPolicy{Policy: prepared, admit: admitBlockReceipts}
}

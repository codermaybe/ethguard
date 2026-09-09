package methods

import (
	"ethguard/rpc"
)

// MethodGetTransactionReceipt is the JSON-RPC method name resolved by
// TransactionReceiptPolicy.
const MethodGetTransactionReceipt = "eth_getTransactionReceipt"

// TransactionReceiptPolicy verifies eth_getTransactionReceipt by full
// agreement. The transaction hash addresses immutable content: no moving
// tags, no anchoring, no freshness machinery — honest sources either hold
// the identical receipt or the disagreement is evidence. A null answer is
// an ordinary answer, so verified absence (no receipt yet) and verified
// presence pass through the same comparison; a fleet split between them is
// conflict. The receipt schema's required-field set binds typed decoding
// after verification, at the root client's decode boundary.
func TransactionReceiptPolicy(opts ...rpc.StrictOption) rpc.Policy {
	return admissionPolicy{Policy: rpc.Strict(normalizeReceiptObject, opts...), admit: admitReceipt}
}

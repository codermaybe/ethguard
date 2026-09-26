package methods

import (
	"context"
	"encoding/json"

	"ethguard/rpc"
	"ethguard/rpc/internal/objects/receipt"
	"ethguard/rpc/internal/policy"
	"ethguard/rpc/internal/wire"
)

// MethodGetTransactionReceipt is the JSON-RPC method name resolved by
// TransactionReceiptPolicy.
const MethodGetTransactionReceipt = "eth_getTransactionReceipt"

// TransactionReceiptPolicy corroborates eth_getTransactionReceipt by full
// agreement. The transaction hash addresses immutable content: no moving
// tags, no anchoring, no freshness machinery — honest sources either hold
// the identical receipt or the disagreement is evidence. A null answer is
// an ordinary answer, so corroborated absence (no receipt yet) and corroborated
// presence pass through the same comparison; a fleet split between them is
// conflict. The receipt schema's required-field set binds typed decoding
// after corroboration, at the root client's decode boundary.
func TransactionReceiptPolicy(opts ...rpc.StrictOption) rpc.Policy {
	return policy.WithAdmission(rpc.Strict(normalizeReceiptObject, compareOpts(rpc.RuleCompareMethodSpecific, opts)...), admitReceipt)
}

func admitReceipt(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receipt.AdmitTransaction(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeReceiptObject(value json.RawMessage) (string, error) {
	return receipt.Normalize(value, wire.PruneMethod, MethodGetTransactionReceipt)
}

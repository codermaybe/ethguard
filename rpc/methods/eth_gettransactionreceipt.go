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
	return policy.WithAdmission(rpc.Strict(normalizeReceiptObject, compareOpts(rpc.RuleCompareMethodSpecific, append([]rpc.StrictOption{rpc.WithCanonicalObject(receipt.Encode)}, opts...))...), admitReceipt)
}

func admitReceipt(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receipt.AdmitTransaction(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeReceiptObject(value json.RawMessage) (string, error) {
	key, err := receipt.Normalize(value, wire.PruneMethod, MethodGetTransactionReceipt)
	if err != nil || key == "null" {
		return key, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &obj); err != nil {
		return "", err
	}
	delete(obj, "from")
	var decoded any
	encoded, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return "", err
	}
	wire.CanonicalizeHex(&decoded)
	folded, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	return string(folded), nil
}

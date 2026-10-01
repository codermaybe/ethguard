package methods

import (
	"context"
	"encoding/json"

	"ethguard/rpc"
	"ethguard/rpc/internal/objects/receipt"
	"ethguard/rpc/internal/policy"
	"ethguard/rpc/internal/wire"
)

// MethodGetBlockReceipts is the JSON-RPC method name resolved by
// BlockReceiptsPolicy.
const MethodGetBlockReceipts = "eth_getBlockReceipts"

// BlockReceiptsPolicy corroborates eth_getBlockReceipts by full agreement over
// the whole receipts array. A block hash determines its receipts fully, so
// the payload is immutable content. Hashes and explicit numbers pass through;
// moving tags are rewritten to the corroborated reference. pending is rejected in
// Prepare because this method has no stable pending-block receipt semantics.
func BlockReceiptsPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	prepared := rpc.RejectPending(0)(rpc.WithAnchor(anchor, 0)(rpc.Strict(normalizeBlockReceiptList, compareOpts(rpc.RuleCompareMethodSpecific, append([]rpc.StrictOption{rpc.WithCanonicalValue(receipt.EncodeBlock)}, opts...))...)))
	return policy.WithAdmission(prepared, admitBlockReceipts)
}

func admitBlockReceipts(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receipt.AdmitBlock(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeBlockReceiptList(value json.RawMessage) (string, error) {
	key, err := receipt.NormalizeBlock(value, wire.PruneMethod, MethodGetBlockReceipts)
	if err != nil || key == "null" {
		return key, err
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &rows); err != nil {
		return "", err
	}
	for _, row := range rows {
		delete(row, "from")
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

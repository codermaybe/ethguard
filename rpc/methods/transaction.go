package methods

import (
	"context"
	"encoding/json"
	"ethguard/rpc"
	"ethguard/rpc/internal/objects/transaction"
)

// This file is the Transaction object slice: the Transaction shape table,
// its semantic gates (hash/signature/inclusion context), and its projection,
// owned by eth_getTransactionByHash. Shared transaction semantics live in
// rpc/internal/objects/transaction; this file keeps the method-facing seam.

// MethodSendRawTransaction is the write-side JSON-RPC method. It has no
// Policy in the read registry: the write pipeline owns its collection and
// adjudication (pool.Broadcast + relative-trust resolution), not the
// method-policy registry. The constant lives here as the transaction
// namespace's method name until the write side gets its own home.
const MethodSendRawTransaction = "eth_sendRawTransaction"

func admitTransaction(ctx context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := transaction.Admit(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

// normalizeTransactionObject admits only a complete transaction object (or
// the protocol's null absence) before it can enter a voting cluster.  The
// transaction hash is checked against the signed payload; placement fields
// are checked as a group so a mined object cannot carry a half-populated
// inclusion context.
func normalizeTransactionObject(value json.RawMessage) (string, error) {
	return transaction.Normalize(MethodGetTransactionByHash, value, PruneMethod)
}

func validateTransactionObject(value json.RawMessage) error {
	return transaction.Validate(value)
}

package methods

import (
	"context"
	"encoding/json"

	"ethguard/rpc"
	"ethguard/rpc/internal/objects/transaction"
	"ethguard/rpc/internal/policy"
	"ethguard/rpc/internal/wire"
)

// MethodGetTransactionByHash is the JSON-RPC method name resolved by
// TransactionByHashPolicy.
const MethodGetTransactionByHash = "eth_getTransactionByHash"

// TransactionByHashPolicy corroborates eth_getTransactionByHash by full
// agreement. The hash addresses immutable content — a mined transaction
// never changes, and a pending transaction's object is stable while it
// stays pending — so it rides the shared canonical-object normalizer. Null
// is an ordinary answer: corroborated absence maps to ethereum.NotFound at the
// typed boundary, and a fleet split between null and an object is conflict.
func TransactionByHashPolicy(opts ...rpc.StrictOption) rpc.Policy {
	strictOpts := append([]rpc.StrictOption{rpc.WithCanonicalObject(transaction.Encode)}, opts...)
	return policy.WithAdmission(rpc.Strict(normalizeTransactionObject, compareOpts(rpc.RuleCompareMethodSpecific, strictOpts)...), admitTransaction)
}

func admitTransaction(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := transaction.Admit(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeTransactionObject(value json.RawMessage) (string, error) {
	return transaction.Normalize(MethodGetTransactionByHash, value, wire.PruneMethod)
}

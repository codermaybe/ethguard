package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/transaction"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func admitTransaction(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := transaction.Admit(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeTransactionObject(value json.RawMessage) (string, error) {
	return transaction.Normalize(MethodGetTransactionByHash, value, wire.PruneMethod)
}

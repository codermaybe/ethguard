package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

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
	encoded, err := json.Marshal(obj)
	return string(encoded), err
}

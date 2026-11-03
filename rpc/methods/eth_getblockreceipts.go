package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

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
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

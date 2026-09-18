package methods

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"ethguard/rpc"
	"ethguard/rpc/internal/objects/receipt"
)

func admitReceipt(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receipt.AdmitTransaction(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func admitBlockReceipts(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receipt.AdmitBlock(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

func requestedHash(req rpc.Request) (common.Hash, error) { return receipt.RequestedHash(req) }
func requestedBlockRef(req rpc.Request) (*common.Hash, *big.Int, error) {
	return receipt.RequestedBlockRef(req)
}
func parseBlockHeight(value any) (*common.Hash, *big.Int, error) {
	return receipt.ParseBlockHeight(value)
}
func normalizeReceiptObject(value json.RawMessage) (string, error) {
	return receipt.Normalize(value, PruneMethod, MethodGetTransactionReceipt)
}
func validateReceiptObject(value json.RawMessage) error { return receipt.Validate(value) }
func normalizeBlockReceiptList(value json.RawMessage) (string, error) {
	return receipt.NormalizeBlock(value, PruneMethod, MethodGetBlockReceipts)
}
func isJSONNull(value json.RawMessage) bool     { return receipt.IsJSONNull(value) }
func presentNonNull(value json.RawMessage) bool { return receipt.PresentNonNull(value) }

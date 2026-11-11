package methods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/transaction"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

const MethodGetTransactionByBlockHashAndIndex = "eth_getTransactionByBlockHashAndIndex"

func transactionInBlockSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetTransactionByBlockHashAndIndex,
		Resolver:               ResolverStrict,
		Normalize:              normalizeTransactionInBlock,
		Encode:                 prepareTransactionInBlock,
		KeySource:              rpc.RuleKeyNormalizer,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Admit:                  admitTransactionInBlock,
		ObservationProjectorID: ProjectorTransaction,
	}
}

func transactionInBlockDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetTransactionByBlockHashAndIndex,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "TransactionInBlock",
		Alignment: "v1.17.10",
	}
}

func admitTransactionInBlock(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	candidate, err := prepareTransactionInBlock(value)
	if err != nil {
		return nil, err
	}
	if err := transaction.AdmitInBlock(req, candidate); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeTransactionInBlock(value json.RawMessage) (string, error) {
	candidate, err := prepareTransactionInBlock(value)
	return string(candidate), err
}

func prepareTransactionInBlock(value json.RawMessage) (json.RawMessage, error) {
	candidate, err := transaction.Encode(value)
	if err != nil || wire.IsJSONNull(candidate) {
		return candidate, err
	}
	var inclusion struct {
		BlockHash json.RawMessage `json:"blockHash"`
	}
	if err := json.Unmarshal(candidate, &inclusion); err != nil {
		return nil, fmt.Errorf("%w: transaction inclusion: %v", rpc.ErrMalformedResponse, err)
	}
	if wire.IsJSONNull(inclusion.BlockHash) {
		return nil, fmt.Errorf("%w: pending transaction has no requested block/index", rpc.ErrMalformedResponse)
	}
	return candidate, nil
}

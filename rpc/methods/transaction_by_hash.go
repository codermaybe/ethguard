package methods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/transaction"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

const MethodGetTransactionByHash = "eth_getTransactionByHash"

func transactionByHashSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetTransactionByHash,
		Resolver:               ResolverStrict,
		Normalize:              normalizeTransactionByHash,
		Encode:                 prepareTransactionByHash,
		KeySource:              rpc.RuleKeyNormalizer,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Admit:                  admitTransactionByHash,
		ObservationProjectorID: ProjectorTransaction,
	}
}

func transactionByHashDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetTransactionByHash,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "TransactionByHash",
		Alignment: "v1.17.10",
	}
}

func admitTransactionByHash(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	candidate, err := prepareTransactionByHash(value)
	if err != nil {
		return nil, err
	}
	if err := transaction.Admit(req, candidate); err != nil {
		return nil, err
	}
	return nil, nil
}

func normalizeTransactionByHash(value json.RawMessage) (string, error) {
	candidate, err := prepareTransactionByHash(value)
	return string(candidate), err
}

func prepareTransactionByHash(value json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(value); err != nil {
		return nil, fmt.Errorf("%w: transaction JSON: %v", rpc.ErrMalformedResponse, err)
	}
	if wire.IsJSONNull(value) {
		return json.RawMessage("null"), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%w: transaction must be an object", rpc.ErrMalformedResponse)
	}
	if _, exists := fields["blockTimestamp"]; !exists {
		corrected, matched, err := correctTransactionByHashKey("blockTimestamp", nil, fields)
		if err != nil {
			return nil, fmt.Errorf("%w: transaction blockTimestamp: %v", rpc.ErrMalformedResponse, err)
		}
		if matched {
			fields["blockTimestamp"] = corrected
		}
	}
	for key, field := range fields {
		corrected, matched, err := correctTransactionByHashKey(key, field, fields)
		if err != nil {
			return nil, fmt.Errorf("%w: transaction %s: %v", rpc.ErrMalformedResponse, key, err)
		}
		if matched {
			fields[key] = corrected
		}
	}
	candidate, err := wire.MarshalNoEscape(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: transaction candidate: %v", rpc.ErrMalformedResponse, err)
	}
	return transaction.Encode(candidate)
}

func correctTransactionByHashKey(key string, value json.RawMessage, fields map[string]json.RawMessage) (json.RawMessage, bool, error) {
	switch key {
	case "blockTimestamp":
		if len(value) == 0 && wire.IsJSONNull(fields["blockHash"]) && wire.IsJSONNull(fields["blockNumber"]) && wire.IsJSONNull(fields["transactionIndex"]) {
			return json.RawMessage("null"), true, nil
		}
		return value, false, nil
	case "hash", "blockHash":
		if key == "blockHash" && wire.IsJSONNull(value) {
			return json.RawMessage("null"), true, nil
		}
		canonical, err := wire.NewHash(value)
		if err != nil {
			return nil, true, err
		}
		encoded, err := canonical.MarshalJSON()
		return encoded, true, err
	case "from", "to":
		if key == "to" && wire.IsJSONNull(value) {
			return json.RawMessage("null"), true, nil
		}
		canonical, err := wire.NewAddress(value)
		if err != nil {
			return nil, true, err
		}
		encoded, err := canonical.MarshalJSON()
		return encoded, true, err
	case "input":
		encoded, err := wire.EncodeData(value)
		return encoded, true, err
	default:
		return value, false, nil
	}
}

package transaction

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// Encode returns the canonical protected transaction result. The execution
// API schema retains the signed transaction fields and the provider's
// inclusion context; that context is corroborating response evidence, not an
// inclusion proof. Admission validates the requested hash and typed payload
// before this encoder is allowed to contribute to a cluster.
func Encode(raw json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, fmt.Errorf("%w: transaction JSON: %v", rpc.ErrMalformedResponse, err)
	}
	key, err := wire.PruneMethod("eth_getTransactionByHash", raw)
	if err != nil {
		return nil, err
	}
	value, err := wire.NewGetTransactionByHashResult(json.RawMessage(key))
	if err != nil {
		var payload types.Transaction
		if payloadErr := json.Unmarshal(json.RawMessage(key), &payload); errors.Is(payloadErr, types.ErrTxTypeNotSupported) {
			return nil, errors.Join(rpc.ErrMalformedResponse, rpc.ErrUnsupportedTypedObject, payloadErr)
		}
		return nil, fmt.Errorf("%w: transaction information: %v", rpc.ErrMalformedResponse, err)
	}
	encoded, err := wire.MarshalNoEscape(value)
	if err != nil {
		return nil, fmt.Errorf("%w: transaction encoding: %v", rpc.ErrMalformedResponse, err)
	}
	var projected, protected map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &projected); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(encoded, &protected); err != nil {
		return nil, err
	}
	if projected != nil {
		pending := IsJSONNull(projected["blockHash"])
		for _, field := range []string{"blockNumber", "blockTimestamp", "transactionIndex"} {
			if IsJSONNull(projected[field]) != pending {
				return nil, fmt.Errorf("%w: inconsistent pending/mined transaction context", rpc.ErrMalformedResponse)
			}
		}
	}
	for field := range projected {
		if _, exists := protected[field]; !exists {
			protected[field] = projected[field]
		}
	}
	return wire.MarshalNoEscape(protected)
}

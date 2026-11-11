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
	// officialFields is the official-mask projection of the raw response
	// (the comparison key); canonicalFields is the typed schema marshal.
	// Merge direction: canonical typed values win; only members the official
	// mask kept but the typed branch did not express are backfilled from the
	// official projection. The transaction branches are officially open
	// objects, so such members are officially tolerated extras whose values
	// carry no schema constraint; members of closed sub-objects (accessList
	// items) never reach here — the mask drops them before the typed parse.
	var officialFields, canonicalFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &officialFields); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(encoded, &canonicalFields); err != nil {
		return nil, err
	}
	if officialFields != nil {
		pending := IsJSONNull(officialFields["blockHash"])
		for _, field := range []string{"blockNumber", "blockTimestamp", "transactionIndex"} {
			if IsJSONNull(officialFields[field]) != pending {
				return nil, fmt.Errorf("%w: inconsistent pending/mined transaction context", rpc.ErrMalformedResponse)
			}
		}
	}
	for field := range officialFields {
		if _, exists := canonicalFields[field]; !exists {
			canonicalFields[field] = officialFields[field]
		}
	}
	return wire.MarshalNoEscape(canonicalFields)
}

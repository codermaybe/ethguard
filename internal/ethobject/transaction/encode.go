package transaction

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

// encodeCanonical prepares the object's representation: the bounded safety
// gate, official-mask projection with hex canonicalization, generated-schema
// parsing (unsupported typed payloads keep their joined sentinel), the
// pending/mined consistency guard, and the canonical merge.
func encodeCanonical(raw json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, fmt.Errorf("%w: transaction JSON: %v", rpc.ErrMalformedResponse, err)
	}
	key, err := wiremask.PruneMethod("eth_getTransactionByHash", raw)
	if err != nil {
		return nil, err
	}
	// Pruning folds hex spelling and can expand strings through HTML escaping.
	// Its output must pass the byte bound before entering generated parsing.
	if err := jsonsafe.Validate([]byte(key)); err != nil {
		return nil, err
	}
	value, err := ethapi.NewGetTransactionByHashResult(json.RawMessage(key))
	if err != nil {
		var payload types.Transaction
		if payloadErr := json.Unmarshal(json.RawMessage(key), &payload); errors.Is(payloadErr, types.ErrTxTypeNotSupported) {
			return nil, errors.Join(rpc.ErrMalformedResponse, rpc.ErrUnsupportedTypedObject, payloadErr)
		}
		return nil, fmt.Errorf("%w: transaction information: %v", rpc.ErrMalformedResponse, err)
	}
	canonicalizeSignatureBytes(value)
	encoded, err := ethcodec.MarshalNoEscape(value)
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
		pending := ethcodec.IsJSONNull(officialFields["blockHash"])
		for _, field := range []string{"blockNumber", "blockTimestamp", "transactionIndex"} {
			if ethcodec.IsJSONNull(officialFields[field]) != pending {
				return nil, fmt.Errorf("%w: inconsistent pending/mined transaction context", rpc.ErrMalformedResponse)
			}
		}
	}
	for field := range officialFields {
		if _, exists := canonicalFields[field]; !exists {
			canonicalFields[field] = officialFields[field]
		}
	}
	return ethcodec.MarshalNoEscape(canonicalFields)
}

// The generated union preserves strings while selecting a complete branch.
// Once validated, only typed signature bytes need a shorter spelling; the
// mask already folded hex casing and projected the closed access-list items.
func canonicalizeSignatureBytes(value *ethapi.Transaction) {
	if value == nil {
		return
	}
	var v, parity *ethapi.HexByte
	switch {
	case value.Signed7702Transaction != nil:
		tx := value.Signed7702Transaction
		v, parity = tx.V, &tx.YParity
		for i := range tx.AuthorizationList {
			canonicalizeHexByte(&tx.AuthorizationList[i].YParity)
		}
	case value.Signed4844Transaction != nil:
		tx := value.Signed4844Transaction
		v, parity = tx.V, &tx.YParity
	case value.Signed1559Transaction != nil:
		tx := value.Signed1559Transaction
		v, parity = tx.V, &tx.YParity
	case value.Signed2930Transaction != nil:
		tx := value.Signed2930Transaction
		v, parity = tx.V, &tx.YParity
	}
	canonicalizeHexByte(v)
	canonicalizeHexByte(parity)
}

// Every value here has passed the generated one-or-two-digit HexByte grammar.
func canonicalizeHexByte(value *ethapi.HexByte) {
	if value != nil && len(*value) == 4 && (*value)[2] == '0' {
		*value = (*value)[:2] + (*value)[3:]
	}
}

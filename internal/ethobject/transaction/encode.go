package transaction

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/ethobject/hexcompat"
	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

// Encode returns the canonical protected transaction result. It is the
// raw entry over the object kernel: the representation work lives
// in encodeCanonical, which Prepare exposes as the immutable Candidate the
// validation and admission bindings reuse. The execution API schema retains
// the signed transaction fields and the provider's inclusion context; that
// context is corroborating response evidence, not an inclusion proof.
// Admission validates the requested hash and typed payload before this
// encoder is allowed to contribute to a cluster.
func Encode(raw json.RawMessage) (json.RawMessage, error) {
	candidate, err := Prepare(raw)
	if err != nil {
		return nil, err
	}
	return candidate.Canonical(), nil
}

// encodeCanonical is the object's representation work, retained verbatim as
// the kernel body Prepare exposes: the bounded safety gate, the official-mask
// projection with its hex canonicalization, the applicable generated-schema
// parse (unsupported typed payloads keep their joined sentinel), the
// pending/mined consistency guard, and the canonical merge.
func encodeCanonical(raw json.RawMessage) (json.RawMessage, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return nil, fmt.Errorf("%w: transaction JSON: %v", rpc.ErrMalformedResponse, err)
	}
	key, err := wiremask.PruneMethod("eth_getTransactionByHash", raw)
	if err != nil {
		return nil, err
	}
	// The existing method mask has already selected and folded this candidate.
	// Typed v/yParity also need their reviewed shortest HexByte representation
	// before the generated union preserves strings for branch selection.
	candidate, err := hexcompat.Transaction(json.RawMessage(key))
	if err != nil {
		return nil, err
	}
	value, err := ethapi.NewGetTransactionByHashResult(candidate)
	if err != nil {
		var payload types.Transaction
		if payloadErr := json.Unmarshal(json.RawMessage(key), &payload); errors.Is(payloadErr, types.ErrTxTypeNotSupported) {
			return nil, errors.Join(rpc.ErrMalformedResponse, rpc.ErrUnsupportedTypedObject, payloadErr)
		}
		return nil, fmt.Errorf("%w: transaction information: %v", rpc.ErrMalformedResponse, err)
	}
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

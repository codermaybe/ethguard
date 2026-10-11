package transaction

import (
	"encoding/json"

	"ethguard/rpc/internal/wire"
)

// Encode returns the canonical protected transaction result. The execution
// API schema retains the signed transaction fields and the provider's
// inclusion context; that context is corroborating response evidence, not an
// inclusion proof. Admission validates the requested hash and typed payload
// before this encoder is allowed to contribute to a cluster.
func Encode(raw json.RawMessage) (json.RawMessage, error) {
	key, err := wire.PruneMethod("eth_getTransactionByHash", raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(key), nil
}

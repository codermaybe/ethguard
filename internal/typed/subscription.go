package typed

import (
	"encoding/json"

	"github.com/ethereum/go-ethereum/core/types"
)

// DecodeHeader shapes one newHeads subscription payload at the shared typed
// boundary. Subscription events are objects; null and malformed payloads are
// rejected instead of being projected as a zero header.
func DecodeHeader(raw json.RawMessage) (*types.Header, error) {
	if jsonNull(raw) {
		return nil, wrapMalformed("decode subscription header: JSON null", nil)
	}
	var header types.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, wrapMalformed("decode subscription header", err)
	}
	return &header, nil
}

// DecodeLog shapes one logs subscription payload at the shared typed
// boundary.
func DecodeLog(raw json.RawMessage) (types.Log, error) {
	if jsonNull(raw) {
		return types.Log{}, wrapMalformed("decode subscription log: JSON null", nil)
	}
	var log types.Log
	if err := json.Unmarshal(raw, &log); err != nil {
		return types.Log{}, wrapMalformed("decode subscription log", err)
	}
	return log, nil
}

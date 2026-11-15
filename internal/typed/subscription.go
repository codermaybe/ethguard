package typed

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/ethapi"
	headerobject "github.com/codermaybe/ethguard/internal/ethobject/header"
	logobject "github.com/codermaybe/ethguard/internal/ethobject/log"
)

// DecodeHeader shapes one newHeads subscription payload at the shared typed
// boundary. The header kernel verifies the declared wire hash against the
// recomputed hash and canonicalizes the official members; the generated
// constructor then owns the schema projection. Subscription events are
// objects; null and malformed payloads are rejected instead of being
// projected as a zero header.
func DecodeHeader(raw json.RawMessage) (*ethapi.Header, error) {
	if jsonNull(raw) {
		return nil, wrapMalformed("decode subscription header: JSON null", nil)
	}
	canonical, err := headerobject.Normalize(raw)
	if err != nil {
		return nil, wrapMalformed("decode subscription header", err)
	}
	header, err := ethapi.NewHeader(canonical)
	if err != nil {
		return nil, wrapMalformed("decode subscription header", err)
	}
	return &header, nil
}

// DecodeLog shapes one logs subscription payload at the shared typed
// boundary.
func DecodeLog(raw json.RawMessage) (ethapi.Log, error) {
	if jsonNull(raw) {
		return ethapi.Log{}, wrapMalformed("decode subscription log: JSON null", nil)
	}
	canonical, err := logobject.EncodeOne(raw)
	if err != nil {
		return ethapi.Log{}, wrapMalformed("decode subscription log", err)
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ethapi.Log{}, wrapMalformed("decode subscription log", err)
	}
	log, err := ethapi.NewLog(encoded)
	if err != nil {
		return ethapi.Log{}, wrapMalformed("decode subscription log", err)
	}
	return log, nil
}

package rpcengine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// FreezeParams performs the one controlled serialization of an executable
// request's parameters. Each element is marshaled exactly once — custom
// MarshalJSON implementations run here and nowhere else — and the frozen
// bytes travel both to the wire and into the prepared-request record, so a
// nondeterministic marshaler cannot produce two different facts.
//
// It returns the executable parameter list (elements frozen as raw JSON) and
// the joined array snapshot for the record. Nil parameters stay nil in both
// results, matching a wire request without a params member. A serialization
// failure is an argument error: the caller fails the operation before any
// fan-out and must not fall back to the unfrozen values.
func FreezeParams(params []any) ([]any, json.RawMessage, error) {
	if params == nil {
		return nil, nil, nil
	}
	frozen := make([]any, len(params))
	parts := make([]string, len(params))
	for i, element := range params {
		raw, err := json.Marshal(element)
		if err != nil {
			return nil, nil, fmt.Errorf("rpc: encode parameter %d: %v: %w", i, err, rpc.ErrInvalidRequest)
		}
		frozen[i] = json.RawMessage(raw)
		parts[i] = string(raw)
	}
	snapshot := json.RawMessage("[" + strings.Join(parts, ",") + "]")
	return frozen, snapshot, nil
}

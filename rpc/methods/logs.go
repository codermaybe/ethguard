package methods

import (
	"bytes"
	"encoding/json"
	"fmt"

	"ethguard/rpc"
	logobject "ethguard/rpc/internal/objects/log"
)

// This file is the Log object slice: Log validation (requiredness) and the
// logs projection.
//
// Ownership split per R2: the generated mask (shared with the receipt slice)
// owns the Log WHITELIST — which fields participate in comparison; this file
// owns Log REQUIREDNESS — which fields must be present for a log to vote.
// Requiredness stays hand-written because the spec union spans variants a
// single response never carries; the whitelist stays generated because it
// must track the standard field-for-field.

func validateLogRequest(req rpc.Request) error {
	if len(req.Params) != 1 {
		return fmt.Errorf("%w: eth_getLogs expects one filter", rpc.ErrInvalidRequest)
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		// Keep the policy usable by the generic anchor tests and callers that
		// perform their own typed decoding. The adapter supplies an object and
		// receives the full filter validation below.
		return nil
	}
	if _, hasHash := filter["blockHash"]; hasHash {
		if _, hasFrom := filter["fromBlock"]; hasFrom {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with fromBlock", rpc.ErrInvalidRequest)
		}
		if _, hasTo := filter["toBlock"]; hasTo {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with toBlock", rpc.ErrInvalidRequest)
		}
	}
	return nil
}

// normalizeLogs validates every log row, then reduces the array to its pruned
// comparison key. Ordering and duplicates stay significant; hexadecimal casing
// and vendor extension fields do not create false disagreement.
func normalizeLogs(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	var rawLogs []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawLogs); err != nil || string(trimmed) == "null" {
		return "", fmt.Errorf("logs: expected an array")
	}
	for i, raw := range rawLogs {
		if err := logobject.Validate(raw); err != nil {
			return "", fmt.Errorf("logs[%d]: %w", i, err)
		}
	}
	return PruneMethod(MethodGetLogs, trimmed)
}

// validateLogsResponse is intentionally stricter than types.Log's JSON
// decoder.  The decoder assigns zero values to omitted fields, which would
// allow an incomplete response to become a valid vote.
func validateLogsResponse(req rpc.Request, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("logs: expected an array")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return fmt.Errorf("logs: expected an array")
	}
	return logobject.ValidateResponse(req, value)
}

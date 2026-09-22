package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"ethguard/rpc"
	logobject "ethguard/rpc/internal/objects/log"
	"ethguard/rpc/internal/wire"
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
	for _, key := range []string{"fromBlock", "toBlock"} {
		value, present := filter[key]
		if !present {
			continue
		}
		if pendingTag(value) {
			return fmt.Errorf("%w: eth_getLogs %s=pending has no stable corroboration semantics", rpc.ErrInvalidRequest, key)
		}
		if err := validateLogBound(key, value); err != nil {
			return err
		}
	}
	if value, present := filter["blockHash"]; present && pendingTag(value) {
		return fmt.Errorf("%w: eth_getLogs blockHash=pending has no stable corroboration semantics", rpc.ErrInvalidRequest)
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

// pendingTag reports spellings that mean the pending tag, including case
// variants and non-string encodings that would otherwise skip the string
// check and reach an anchor or a provider.
func pendingTag(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "pending")
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return false
		}
		return pendingTag(decoded)
	case map[string]any:
		number, ok := typed["blockNumber"]
		return ok && pendingTag(number)
	default:
		return false
	}
}

// validateLogBound rejects empty and wrong-typed range fields locally.
// Recognized non-pending tags and quantities pass through for anchoring.
func validateLogBound(key string, value any) error {
	switch typed := value.(type) {
	case nil:
		return fmt.Errorf("%w: eth_getLogs %s is empty", rpc.ErrInvalidRequest, key)
	case string:
		if strings.TrimSpace(typed) == "" {
			return fmt.Errorf("%w: eth_getLogs %s is empty", rpc.ErrInvalidRequest, key)
		}
		return nil
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return fmt.Errorf("%w: eth_getLogs %s has an unsupported type", rpc.ErrInvalidRequest, key)
		}
		if pendingTag(decoded) {
			return fmt.Errorf("%w: eth_getLogs %s=pending has no stable corroboration semantics", rpc.ErrInvalidRequest, key)
		}
		return validateLogBound(key, decoded)
	default:
		return fmt.Errorf("%w: eth_getLogs %s has an unsupported type", rpc.ErrInvalidRequest, key)
	}
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
	return wire.PruneMethod(MethodGetLogs, trimmed)
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

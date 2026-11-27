package ethcodec

import (
	"bytes"
	"encoding/json"
	"strings"
)

// FoldHexSpelling folds only the prefix and hex digit case of one JSON
// string selected by its owner as a hexadecimal field. It preserves width
// and leading zeros. Plain lowercase inputs reuse their original bytes.
// Non-strings, malformed JSON and non-hex strings pass through unchanged so
// the following schema validator, not this compatibility step, rejects them.
// Callers must not apply this to free text, objects or unknown fields.
func FoldHexSpelling(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '"' || !bytes.ContainsAny(trimmed, "XABCDEF\\") {
		return raw
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) < 2 || !strings.EqualFold(value[:2], "0x") {
		return raw
	}
	for _, digit := range value[2:] {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') || (digit >= 'A' && digit <= 'F')) {
			return raw
		}
	}
	canonical := strings.ToLower(value)
	if canonical == value {
		return raw
	}
	// The checked alphabet needs no JSON escaping.
	return json.RawMessage(`"` + canonical + `"`)
}

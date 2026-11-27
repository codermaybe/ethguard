package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
)

// IsPendingReference reports pending spellings at one block-reference
// position: case-insensitive strings (with surrounding space), JSON strings,
// and EIP-1898 objects whose blockNumber is pending. A blockHash object is
// not pending. This is the single pending-grammar authority — the refuse
// path (PendingRejectAt), the observe path (the aggregator's local-shape
// check), and the methods package's request validation all route through it,
// so one spelling cannot be pending in one mode and remote in another.
func IsPendingReference(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "pending")
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return false
		}
		return IsPendingReference(decoded)
	case map[string]any:
		number, ok := typed["blockNumber"]
		return ok && IsPendingReference(number)
	default:
		return false
	}
}

package methods

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// normalizeSyncing admits the two wire shapes the method defines: false for a
// fully synced node, or a progress object. JSON null, booleans other than
// false, and scalars are malformed answers, not local truth.
func normalizeSyncing(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("false")) {
		return "false", nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", fmt.Errorf("syncing: expected false or an object")
	}
	return rpc.CanonicalJSON(value)
}

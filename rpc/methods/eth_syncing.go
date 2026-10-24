package methods

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// MethodSyncing is the JSON-RPC method name resolved by SyncingPolicy.
const MethodSyncing = "eth_syncing"

// SyncingPolicy resolves eth_syncing as an upstream-local result: the
// answer describes one provider's own sync state, so it is selected, not
// compared (see rpc.Local).
func SyncingPolicy() rpc.Policy {
	return rpc.Local(normalizeSyncing, rpc.WithLocalRuleCompareKind(rpc.RuleCompareObject), rpc.WithLocalCanonical(rpc.EncodeCanonicalJSON))
}

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

package methods

import (
	"ethguard/rpc"
)

// MethodSyncing is the JSON-RPC method name resolved by SyncingPolicy.
const MethodSyncing = "eth_syncing"

// SyncingPolicy resolves eth_syncing as an upstream-local result: the
// answer describes one provider's own sync state, so it is selected, not
// compared (see rpc.Local).
func SyncingPolicy() rpc.Policy {
	return rpc.Local(rpc.CanonicalJSON, rpc.WithLocalRuleCompareKind(rpc.RuleCompareObject))
}

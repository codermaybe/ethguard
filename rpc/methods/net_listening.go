package methods

import (
	"ethguard/rpc"
)

// MethodListening is the JSON-RPC method name resolved by ListeningPolicy.
const MethodListening = "net_listening"

// ListeningPolicy resolves net_listening as an upstream-local result: whether
// a node accepts inbound peers describes one provider alone (see rpc.Local).
func ListeningPolicy() rpc.Policy {
	return rpc.Local(rpc.CanonicalJSON, rpc.WithLocalRuleCompareKind(rpc.RuleCompareObject))
}

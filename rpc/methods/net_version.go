package methods

import (
	"ethguard/rpc"
)

// MethodNetVersion is the JSON-RPC method name resolved by NetVersionPolicy.
const MethodNetVersion = "net_version"

// NetVersionPolicy verifies net_version by full agreement. The answer is the
// chain identity in its legacy decimal spelling — a separate encoder from
// eth_chainId's hex quantity, so it gets its own normalizer rather than
// sharing one.
func NetVersionPolicy() rpc.Policy { return rpc.Strict(normalizeDecimalString) }

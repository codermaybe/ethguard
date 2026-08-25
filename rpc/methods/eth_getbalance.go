package methods

import (
	"ethguard/rpc"
)

// MethodGetBalance is the JSON-RPC method name resolved by GetBalancePolicy.
const MethodGetBalance = "eth_getBalance"

// GetBalancePolicy verifies balances read at an agreed block reference.
// Sources sit at different heads; comparing their answers to a moving tag is
// noise. The policy rewrites latest/safe/finalized to a verified block hash
// before fan-out, so all sources answer for identical state. The anchor is
// injected by the composition root.
func GetBalancePolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return rpc.Anchored(1, normalizeHexQuantity, anchor)
}

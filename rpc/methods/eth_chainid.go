package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodChainID is the JSON-RPC method name resolved by ChainIDPolicy.
const MethodChainID = "eth_chainId"

// ChainIDPolicy corroborates eth_chainId by full agreement.
func ChainIDPolicy(opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Strict(wire.NormalizeWireQuantity, compareOpts(rpc.RuleCompareQuantity, opts)...)
}

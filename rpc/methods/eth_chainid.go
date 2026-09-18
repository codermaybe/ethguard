package methods

import (
	"ethguard/rpc"
)

// MethodChainID is the JSON-RPC method name resolved by ChainIDPolicy.
const MethodChainID = "eth_chainId"

// ChainIDPolicy verifies eth_chainId by full agreement.
func ChainIDPolicy(opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Strict(normalizeWireQuantity, opts...)
}

package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodChainID is the JSON-RPC method name resolved by ChainIDPolicy.
const MethodChainID = "eth_chainId"

// ChainIDPolicy corroborates eth_chainId by full agreement.
func ChainIDPolicy(opts ...rpc.StrictOption) rpc.Policy {
	opts = append([]rpc.StrictOption{rpc.WithCanonicalValue(wire.EncodeQuantity)}, opts...)
	return rpc.Strict(wire.NormalizeWireQuantity, compareOpts(rpc.RuleCompareQuantity, opts)...)
}

package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodNetVersion is the JSON-RPC method name resolved by NetVersionPolicy.
const MethodNetVersion = "net_version"

// NetVersionPolicy corroborates net_version by full agreement. The answer is the
// chain identity in its legacy decimal spelling — a separate encoder from
// eth_chainId's hex quantity, so it gets its own normalizer rather than
// sharing one.
func NetVersionPolicy(opts ...rpc.StrictOption) rpc.Policy {
	opts = append([]rpc.StrictOption{rpc.WithCanonicalValue(wire.EncodeDecimal)}, opts...)
	return rpc.Strict(wire.NormalizeDecimalString, compareOpts(rpc.RuleCompareQuantity, opts)...)
}

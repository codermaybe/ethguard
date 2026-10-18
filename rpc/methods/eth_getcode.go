package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetCode is the JSON-RPC method name resolved by GetCodePolicy.
const MethodGetCode = "eth_getCode"

// GetCodePolicy corroborates eth_getCode at an agreed block reference. The
// answer is arbitrary-length bytecode — hex data, not a quantity; an
// externally owned account answers "0x", which is valid data and would
// fail a quantity-shaped normalizer. Strict options pass through to the
// underlying strict resolution. Moving tags are anchored; explicit block
// numbers, hashes, and pending pass through unchanged. EIP-1898 object
// forms ride EIP1898BlockRef (see GetBalancePolicy). Pending resolves as a
// node-local observation (see GetBalancePolicy).
func GetCodePolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	opts = append([]rpc.StrictOption{rpc.WithCanonicalValue(wire.EncodeData)}, opts...)
	return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.Anchored(1, wire.NormalizeHexData, anchor, compareOpts(rpc.RuleCompareMethodSpecific, opts)...)))
}

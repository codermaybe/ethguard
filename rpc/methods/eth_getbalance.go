package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetBalance is the JSON-RPC method name resolved by GetBalancePolicy.
const MethodGetBalance = "eth_getBalance"

// GetBalancePolicy corroborates balances read at an agreed block reference.
// Sources sit at different heads; comparing their answers to a moving tag is
// noise. The policy rewrites latest/safe/finalized to a corroborated block hash
// before fan-out, so all sources answer for identical state. The anchor is
// injected by the composition root; strict options pass through so client
// level quorum tuning reaches anchored state reads too. Explicit block
// numbers, hashes, and pending pass through unchanged; pending is supported
// as an explicit provider state request rather than a moving tag.
// EIP-1898 object forms ({blockHash} / {blockNumber[, requireCanonical]})
// are admitted by EIP1898BlockRef: only the official default-block methods
// carry this layer, never quantity-only positions. Pending is served as a
// node-local observation by PendingLocal (decision 0035): it keeps its
// node-state meaning and can never become Corroborated through agreement.
func GetBalancePolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	opts = append([]rpc.StrictOption{rpc.WithCanonicalValue(wire.EncodeQuantity)}, opts...)
	return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.Anchored(1, wire.NormalizeWireQuantity, anchor, compareOpts(rpc.RuleCompareQuantity, opts)...)))
}

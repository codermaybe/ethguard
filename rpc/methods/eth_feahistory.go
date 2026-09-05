package methods

import (
	"ethguard/rpc"
)

// MethodFeeHistory is the JSON-RPC method name resolved by FeeHistoryPolicy.
const MethodFeeHistory = "eth_feeHistory"

// FeeHistoryPolicy verifies eth_feeHistory by full agreement over the whole
// response. The range is chain-determined once newest is pinned: the base
// fees and gas ratios of historical blocks are immutable, the block count
// is the caller's fixed input, and the newest+1 entry is the EIP-1559
// prediction derived from the newest block itself.
//
// The wire form constrains the anchor shape: feeHistory's newestBlock is a
// BlockNumber, not a BlockNumberOrHash, so a 32-byte hash cannot ride the
// wire — the moving tag is rewritten to the verified reference's height
// instead (see rpc.AnchorHeightFunc). Cross-client rounding of the reward
// percentiles is the known liveness gate: until a soak measures it, rounding
// differences fail closed as conflict.
func FeeHistoryPolicy(height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.WithAnchorHeight(height, 1)(rpc.Strict(normalizeCanonicalObject, opts...))
}

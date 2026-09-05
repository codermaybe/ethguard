package methods

import (
	"ethguard/rpc"
)

// MethodGetLogs is the JSON-RPC method name resolved by LogsPolicy.
const MethodGetLogs = "eth_getLogs"

// LogsPolicy verifies eth_getLogs by full agreement over the whole log
// array. Once the range is pinned the response is chain-determined and
// strictly comparable (analysis 2026-09-04): every log is ordered by its
// chain-assigned index, so benign encoding differences are erased by the
// canonical-object normalizer and whatever still differs is dissent.
//
// The range lives in the filter object's fromBlock/toBlock fields, and the
// wire types those fields as BlockNumbers — a 32-byte hash dies there with
// "hex number > 64 bits" (the soak's first catch; blockHash is the
// dedicated, mutually exclusive hash position). The bounds are therefore
// pinned to the verified reference's height: moving tags are rewritten and
// absent fields are defaulted, since a provider's implicit "latest" is a
// moving tag in field clothing. blockHash pins pass through untouched.
//
// Payload cost makes getLogs a singleflight candidate (deferred);
// pruned-node gaps surface as dissent and fail closed — excluding stale
// reporters stays a deferred liveness knob, the same shape as decision
// 0016.
func LogsPolicy(height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.WithAnchorHeightFields(height, 0, "fromBlock", "toBlock")(
		rpc.Strict(normalizeCanonicalObject, opts...))
}

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
// The range lives in the filter object's fromBlock/toBlock fields, so the
// rewrite is field-shaped: moving tags become the verified reference and
// absent fields are defaulted to it — a provider's implicit "latest" is a
// moving tag in field clothing, and comparing un-pinned ranges would turn
// routine head divergence into false conflict. blockHash pins the range
// explicitly and passes through untouched. Payload cost makes getLogs a
// singleflight candidate (deferred); pruned-node gaps surface as dissent
// and fail closed — excluding stale reporters stays a deferred liveness
// knob, the same shape as decision 0016.
func LogsPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return rpc.WithAnchorFields(anchor, 0, "fromBlock", "toBlock")(
		rpc.Strict(normalizeCanonicalObject, opts...))
}

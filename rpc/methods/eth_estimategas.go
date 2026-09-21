package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodEstimateGas is the JSON-RPC method name resolved by
// EstimateGasPolicy.
const MethodEstimateGas = "eth_estimateGas"

// EstimateGasPolicy resolves eth_estimateGas as an anchored variable result:
// the fleet's lower median at the corroborated block reference. Estimates never
// claim Corroborated (ruling 2026-09-04) — agreeing on an estimate is
// coincidence, not corroboration of a deterministic truth. Sources whose
// estimation fails (the call would revert, or the provider rejected the
// exchange) ride as faults; a fleet that unanimously fails fails as
// no-valid-response, so "this call reverts" is never laundered into a
// number.
func EstimateGasPolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return rpc.WithAnchor(anchor, 1)(rpc.Median(wire.NormalizeWireQuantity))
}

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
//
// Position 1 accepts the same EIP-1898 shapes as the T02 state reads.
// String moving tags still rewrite to the bare anchor hash. Object moving
// tags rewrite to {blockHash: anchor[, requireCanonical]}. Explicit
// quantities, hashes, and earliest pass through. Pending — string or
// object — is a node-local observation: it passes through, never touches
// the anchor, and the first healthy source stands, ceiling Selected, never
// Corroborated. That semantic belongs to PendingLocal, not to Median.
// Median still resolves every other shape and never claims Corroborated.
// Malformed objects fail locally before any anchor call.
func EstimateGasPolicy(anchor rpc.AnchorFunc) rpc.Policy {
	return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.WithAnchor(anchor, 1)(rpc.Median(wire.NormalizeWireQuantity, rpc.WithMedianCanonical(wire.EncodeQuantity)))))
}

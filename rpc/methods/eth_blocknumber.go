package methods

import (
	"ethguard/rpc"
)

// MethodBlockNumber is the JSON-RPC method name resolved by BlockNumberPolicy.
const MethodBlockNumber = "eth_blockNumber"

// BlockNumberPolicy resolves eth_blockNumber as a variable result: the fleet's
// lower median (see rpc.Median), aligned with pool tip tracking semantics.
// Disagreement between sources reflects slight block propagation timing
// differences rather than Byzantine dissent.
func BlockNumberPolicy() rpc.Policy { return rpc.Median(normalizeHexQuantity) }

package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodGasPrice is the JSON-RPC method name resolved by GasPricePolicy.
const MethodGasPrice = "eth_gasPrice"

// GasPricePolicy resolves gas prices as a variable result: the fleet's lower
// median, never claimed verified (see rpc.Median). Sources legitimately sit
// at different moments and pricing policies; disagreement is spread
// evidence, not dissent.
func GasPricePolicy() rpc.Policy { return rpc.Median(wire.NormalizeWireQuantity) }

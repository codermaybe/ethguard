package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MaxPriorityFeePerGasPolicy resolves eth_maxPriorityFeePerGas as a variable
// result: the fleet's lower median, never claimed corroborated (see
// GasPricePolicy — same class, different moment of the fee market).
func MaxPriorityFeePerGasPolicy() rpc.Policy {
	return rpc.Median(wire.NormalizeWireQuantity, rpc.WithMedianCanonical(wire.EncodeQuantity))
}

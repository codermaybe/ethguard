package methods

import (
	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodBlobBaseFee is the JSON-RPC method name resolved by BlobBaseFeePolicy.
const MethodBlobBaseFee = "eth_blobBaseFee"

// BlobBaseFeePolicy resolves EIP-4844 blob base fee as a variable result:
// the fleet's lower median, consistent with fee estimation policies
// (GasPricePolicy, MaxPriorityFeePerGasPolicy). Sources legitimately observe
// slightly different block arrival moments and local pricing states.
func BlobBaseFeePolicy() rpc.Policy {
	return rpc.Median(wire.NormalizeWireQuantity, rpc.WithMedianCanonical(wire.EncodeQuantity))
}

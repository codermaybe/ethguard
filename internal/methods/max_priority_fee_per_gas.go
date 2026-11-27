package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/rpcengine"
)

// MethodMaxPriorityFeePerGas is the JSON-RPC method spelling this file owns:
// the priority-fee suggestion.
const MethodMaxPriorityFeePerGas = "eth_maxPriorityFeePerGas"

// maxPriorityFeePerGasSpec is the method's compiled contract: quantity
// sampling over the median resolver; the median resolver pins its
// comparison-key class internally. No whole-key compatibility correction
// applies to this method.
func maxPriorityFeePerGasSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodMaxPriorityFeePerGas,
		Resolver:    ResolverMedian,
		Normalize:   ethcodec.NormalizeWireQuantity,
		Encode:      ethcodec.EncodeQuantity,
		CompareKind: rpcengine.RuleCompareQuantity,
	}
}

func maxPriorityFeePerGasDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodMaxPriorityFeePerGas,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "SuggestGasTipCap",
		Alignment: "v1.17.10",
	}
}

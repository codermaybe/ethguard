package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodGasPrice is the JSON-RPC method spelling this file owns: the node's
// price suggestion.
const MethodGasPrice = "eth_gasPrice"

// gasPriceSpec is the method's compiled contract: quantity sampling over the
// median resolver; the median resolver pins its comparison-key class
// internally. No whole-key compatibility correction applies to this method.
func gasPriceSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGasPrice,
		Resolver:    ResolverMedian,
		Normalize:   ethcodec.NormalizeWireQuantity,
		Encode:      ethcodec.EncodeQuantity,
		CompareKind: rpc.RuleCompareQuantity,
	}
}

func gasPriceDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGasPrice,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "SuggestGasPrice",
		Alignment: "v1.17.10",
	}
}

package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodEstimateGas is the JSON-RPC method spelling this file owns: the gas
// estimate read.
const MethodEstimateGas = "eth_estimateGas"

// estimateGasSpec is the method's compiled contract: quantity sampling over
// the median resolver with a moving reference riding the block parameter.
// Provider revert shapes stay provider-local estimates, not execution
// observations. No whole-key compatibility correction applies to this
// method.
func estimateGasSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodEstimateGas,
		Resolver:    ResolverMedian,
		Normalize:   ethcodec.NormalizeWireQuantity,
		Encode:      wire.EncodeQuantity,
		CompareKind: rpc.RuleCompareQuantity,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
	}
}

func estimateGasDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodEstimateGas,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "EstimateGas",
		Alignment: "v1.17.10",
	}
}

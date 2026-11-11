package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodBlobBaseFee is the JSON-RPC method spelling this file owns: the
// blob base-fee estimate.
const MethodBlobBaseFee = "eth_blobBaseFee"

// blobBaseFeeSpec is the method's compiled contract: quantity sampling over
// the median resolver; the median resolver pins its comparison-key class
// internally. No whole-key compatibility correction applies to this method.
func blobBaseFeeSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodBlobBaseFee,
		Resolver:    ResolverMedian,
		Normalize:   wire.NormalizeWireQuantity,
		Encode:      wire.EncodeQuantity,
		CompareKind: rpc.RuleCompareQuantity,
	}
}

func blobBaseFeeDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodBlobBaseFee,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Alignment: "v1.17.10",
	}
}

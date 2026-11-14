package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodBlockNumber is the JSON-RPC method spelling this file owns: the
// head height read.
const MethodBlockNumber = "eth_blockNumber"

// blockNumberSpec is the method's compiled contract: quantity sampling over
// the median resolver; the median resolver pins its comparison-key class
// internally. No whole-key compatibility correction applies to this method.
func blockNumberSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodBlockNumber,
		Resolver:    ResolverMedian,
		Normalize:   ethcodec.NormalizeWireQuantity,
		Encode:      wire.EncodeQuantity,
		CompareKind: rpc.RuleCompareQuantity,
	}
}

func blockNumberDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodBlockNumber,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "BlockNumber",
		Alignment: "v1.17.10",
	}
}

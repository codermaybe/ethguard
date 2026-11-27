package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodGetBlockTransactionCountByNumber is the JSON-RPC method spelling
// this file owns: the block transaction count addressed by height or tag.
const MethodGetBlockTransactionCountByNumber = "eth_getBlockTransactionCountByNumber"

// blockTransactionCountByNumberSpec is the method's compiled contract:
// canonical quantity comparison over the strict resolver, a moving reference
// riding the height parameter, and pending observed as a source-local read.
// No whole-key compatibility correction applies to this method.
func blockTransactionCountByNumberSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGetBlockTransactionCountByNumber,
		Resolver:    ResolverStrict,
		Normalize:   ethcodec.NormalizeWireQuantity,
		Encode:      ethcodec.EncodeQuantity,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareQuantity,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{0}},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 0},
	}
}

func blockTransactionCountByNumberDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetBlockTransactionCountByNumber,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "PendingTransactionCount",
		Alignment: "v1.17.10",
	}
}

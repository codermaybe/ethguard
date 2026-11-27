package methods

import (
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/rpcengine"
)

// MethodGetStorageAt is the JSON-RPC method spelling this file owns: the
// contract storage slot read.
const MethodGetStorageAt = "eth_getStorageAt"

// getStorageAtSpec is the method's compiled contract: canonical storage-word
// comparison over the strict resolver, a moving reference riding the block
// parameter, and pending observed as a source-local read. No whole-key
// compatibility correction applies to this method.
func getStorageAtSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGetStorageAt,
		Resolver:    ResolverStrict,
		Normalize:   standardDataResult(ethapi.NewGetStorageAtResult, 32),
		Encode:      standardDataEncoding(ethapi.NewGetStorageAtResult, 32),
		KeySource:   rpcengine.RuleKeyCanonicalBytes,
		CompareKind: rpcengine.RuleCompareMethodSpecific,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{2}, EIP1898: true},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 2},
	}
}

func getStorageAtDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetStorageAt,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "StorageAt",
		Alignment: "v1.17.10",
	}
}

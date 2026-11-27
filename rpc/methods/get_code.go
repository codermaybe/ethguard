package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetCode is the JSON-RPC method spelling this file owns: the account
// code read.
const MethodGetCode = "eth_getCode"

// getCodeSpec is the method's compiled contract: canonical DATA comparison
// over the strict resolver, a moving reference riding the block parameter,
// and pending observed as a source-local read. No whole-key compatibility
// correction applies to this method.
func getCodeSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGetCode,
		Resolver:    ResolverStrict,
		Normalize:   ethcodec.NormalizeHexData,
		Encode:      wire.EncodeData,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareMethodSpecific,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
	}
}

func getCodeDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetCode,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "CodeAt",
		Alignment: "v1.17.10",
	}
}

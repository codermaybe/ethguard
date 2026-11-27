package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetTransactionCount is the JSON-RPC method spelling this file owns:
// the account nonce read. It is distinct from
// eth_getBlockTransactionCountByHash, which counts transactions in a block.
const MethodGetTransactionCount = "eth_getTransactionCount"

// getTransactionCountSpec is the method's compiled contract: canonical
// quantity comparison over the strict resolver, a moving reference riding
// the block parameter, and pending observed as a source-local read. No
// whole-key compatibility correction applies to this method.
func getTransactionCountSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGetTransactionCount,
		Resolver:    ResolverStrict,
		Normalize:   ethcodec.NormalizeWireQuantity,
		Encode:      wire.EncodeQuantity,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareQuantity,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
	}
}

func getTransactionCountDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetTransactionCount,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "NonceAt",
		Alignment: "v1.17.10",
	}
}

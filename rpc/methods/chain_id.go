package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodChainID is the JSON-RPC method spelling this file owns: the chain
// identity read.
const MethodChainID = "eth_chainId"

// chainIDSpec is the method's compiled contract: canonical quantity
// comparison over the strict resolver, no reference, no pending handling.
// No whole-key compatibility correction applies to this method.
func chainIDSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodChainID,
		Resolver:    ResolverStrict,
		Normalize:   wire.NormalizeWireQuantity,
		Encode:      wire.EncodeQuantity,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareQuantity,
	}
}

func chainIDDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodChainID,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "ChainID",
		Alignment: "v1.17.10",
	}
}

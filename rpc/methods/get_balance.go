package methods

import (
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetBalance is the JSON-RPC method spelling this file owns: the
// account balance read.
const MethodGetBalance = "eth_getBalance"

// getBalanceSpec is the method's compiled contract: canonical quantity
// comparison over the strict resolver, a moving reference riding the block
// parameter, and pending observed as a source-local read. No whole-key
// compatibility correction applies to this method.
func getBalanceSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetBalance,
		Resolver:               ResolverStrict,
		Normalize:              ethcodec.NormalizeWireQuantity,
		Encode:                 wire.EncodeQuantity,
		KeySource:              rpc.RuleKeyCanonicalBytes,
		CompareKind:            rpc.RuleCompareQuantity,
		Reference:              ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:                PendingSpec{Mode: PendingObserve, Pos: 1},
		ObservationProjectorID: ProjectorScalarStandard,
	}
}

func getBalanceDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetBalance,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "BalanceAt",
		Alignment: "v1.17.10",
	}
}

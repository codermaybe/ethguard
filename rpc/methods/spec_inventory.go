package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// Specs is the frozen MethodSpec inventory: one entry per Registered read
// method, each declaring resolver, value domain, reference need, pending
// handling, hooks, and projector. Compile turns it into the registry the
// composition root installs; the hand-written constructors remain the
// public surface the equivalence tests hold together with these specs.
func Specs() []MethodSpec {
	return []MethodSpec{
		chainIDSpec(),
		getBlockByHashSpec(),
		getBlockByNumberSpec(),
		getBalanceSpec(),
		getTransactionReceiptSpec(),
		transactionByHashSpec(),
		transactionInBlockSpec(),
		getBlockReceiptsSpec(),
		getTransactionCountSpec(),
		blockTransactionCountByHashSpec(),
		blockTransactionCountByNumberSpec(),
		getStorageAtSpec(),
		getCodeSpec(),
		{
			Method:                 MethodCall,
			Resolver:               ResolverCallRevert,
			Normalize:              normalizeCallOutcome,
			CompareKind:            rpc.RuleCompareMethodSpecific,
			Reference:              ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:                PendingSpec{Mode: PendingObserve, Pos: 1},
			ObservationProjectorID: ProjectorScalarStandard,
		},
		{
			Method:      MethodEstimateGas,
			Resolver:    ResolverMedian,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			CompareKind: rpc.RuleCompareQuantity,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
		},
		getLogsSpec(),
		feeHistorySpec(),
		gasPriceSpec(),
		maxPriorityFeePerGasSpec(),
		blobBaseFeeSpec(),
		blockNumberSpec(),
		createAccessListSpec(),
		peerCountSpec(),
		syncingSpec(),
		netVersionSpec(),
		listeningSpec(),
	}
}

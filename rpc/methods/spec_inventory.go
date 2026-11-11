package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// The spec inventory reuses each method's own admission hooks. The block
// methods bind their identity explicitly so plan and compatibility admission
// cannot drift through request-method inference.
func admitBlockByHashResponse(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, true, call)
}

func admitBlockByNumberResponse(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, false, call)
}

// Specs is the frozen MethodSpec inventory: one entry per Registered read
// method, each declaring resolver, value domain, reference need, pending
// handling, hooks, and projector. Compile turns it into the registry the
// composition root installs; the hand-written constructors remain the
// public surface the equivalence tests hold together with these specs.
func Specs() []MethodSpec {
	return []MethodSpec{
		chainIDSpec(),
		{
			Method:                 MethodGetBlockByHash,
			Resolver:               ResolverBlock,
			ByHash:                 true,
			Normalize:              normalizeBlockIdentity,
			Encode:                 blockobject.Encode,
			KeySource:              rpc.RuleKeyNormalizer,
			CompareKind:            rpc.RuleCompareMethodSpecific,
			Admit:                  admitBlockByHashResponse,
			ObservationProjectorID: ProjectorBlock,
		},
		{
			Method:                 MethodGetBlockByNumber,
			Resolver:               ResolverBlock,
			Normalize:              normalizeBlockIdentity,
			Encode:                 blockobject.EncodeByNumber,
			KeySource:              rpc.RuleKeyNormalizer,
			CompareKind:            rpc.RuleCompareMethodSpecific,
			Pending:                PendingSpec{Mode: PendingObserve, Pos: 0},
			Admit:                  admitBlockByNumberResponse,
			ObservationProjectorID: ProjectorBlock,
		},
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

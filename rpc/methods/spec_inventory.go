package methods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	accesslist "github.com/codermaybe/ethguard/rpc/internal/objects/accesslist"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
	feehistory "github.com/codermaybe/ethguard/rpc/internal/objects/feehistory"
	logobject "github.com/codermaybe/ethguard/rpc/internal/objects/log"
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

func admitLogs(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateLogsResponse(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
}

func admitFeeHistory(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateFeeHistoryRequest(req); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	if _, err := normalizeFeeHistory(value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	if err := validateFeeHistoryRange(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
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
		{
			Method:                 MethodGetLogs,
			Resolver:               ResolverStrict,
			Normalize:              normalizeLogs,
			Encode:                 logobject.Encode,
			KeySource:              rpc.RuleKeyCanonicalBytes,
			CompareKind:            rpc.RuleCompareMethodSpecific,
			Reference:              ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}},
			Pending:                PendingSpec{Mode: PendingRefuse, Pos: 0},
			Validate:               func(_ context.Context, req rpc.Request) error { return validateLogRequest(req) },
			Admit:                  admitLogs,
			ObservationProjectorID: ProjectorLogs,
		},
		{
			Method:                 MethodFeeHistory,
			Resolver:               ResolverStrict,
			Normalize:              normalizeFeeHistory,
			Encode:                 feehistory.Encode,
			KeySource:              rpc.RuleKeyCanonicalBytes,
			CompareKind:            rpc.RuleCompareMethodSpecific,
			Reference:              ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{1}},
			Pending:                PendingSpec{Mode: PendingObserve, Pos: 1},
			Validate:               func(_ context.Context, req rpc.Request) error { return validateFeeHistoryRequest(req) },
			Admit:                  admitFeeHistory,
			ObservationProjectorID: ProjectorFeeHistory,
		},
		gasPriceSpec(),
		maxPriorityFeePerGasSpec(),
		blobBaseFeeSpec(),
		blockNumberSpec(),
		{
			Method:      MethodCreateAccessList,
			Resolver:    ResolverStrict,
			Normalize:   normalizeAccessList,
			Encode:      accesslist.Encode,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
		},
		peerCountSpec(),
		{
			Method:      MethodSyncing,
			Resolver:    ResolverLocal,
			Normalize:   normalizeSyncing,
			Encode:      rpc.EncodeCanonicalJSON,
			CompareKind: rpc.RuleCompareObject,
		},
		netVersionSpec(),
		listeningSpec(),
	}
}

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
	receiptobject "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	transactionobject "github.com/codermaybe/ethguard/rpc/internal/objects/transaction"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// The spec inventory reuses each method's own admission hooks. The block
// methods share the block object's admission with their byHash identity.
func admitBlock(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if string(value) == "null" {
		return nil, nil
	}
	fullTx := len(req.Params) > 1 && req.Params[1] == true
	if !fullTx {
		return nil, nil
	}
	_, byHash := map[string]bool{MethodGetBlockByHash: true}[req.Method]
	return blockobject.AdmitBlock(ctx, req, value, byHash, call)
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
		{
			Method:      MethodChainID,
			Resolver:    ResolverStrict,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareQuantity,
		},
		{
			Method:      MethodGetBlockByHash,
			Resolver:    ResolverBlock,
			ByHash:      true,
			Normalize:   normalizeBlockIdentity,
			Encode:      blockobject.Encode,
			KeySource:   rpc.RuleKeyNormalizer,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Admit:       admitBlock,
		},
		{
			Method:      MethodGetBlockByNumber,
			Resolver:    ResolverBlock,
			Normalize:   normalizeBlockIdentity,
			Encode:      blockobject.EncodeByNumber,
			KeySource:   rpc.RuleKeyNormalizer,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 0},
			Admit:       admitBlock,
		},
		{
			Method:      MethodGetBalance,
			Resolver:    ResolverStrict,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareQuantity,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
		},
		{
			Method:      MethodGetTransactionReceipt,
			Resolver:    ResolverStrict,
			Normalize:   normalizeReceiptObject,
			Encode:      receiptobject.Encode,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Admit:       admitReceipt,
		},
		{
			Method:      MethodGetTransactionByHash,
			Resolver:    ResolverStrict,
			Normalize:   normalizeTransactionObject,
			Encode:      transactionobject.Encode,
			KeySource:   rpc.RuleKeyNormalizer,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Admit:       admitTransaction,
		},
		{
			Method:      MethodGetBlockReceipts,
			Resolver:    ResolverStrict,
			Normalize:   normalizeBlockReceiptList,
			Encode:      receiptobject.EncodeBlock,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{0}},
			Pending:     PendingSpec{Mode: PendingRefuse, Pos: 0},
			Admit:       admitBlockReceipts,
		},
		{
			Method:      MethodGetTransactionCount,
			Resolver:    ResolverStrict,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareQuantity,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
		},
		{
			Method:      MethodGetStorageAt,
			Resolver:    ResolverStrict,
			Normalize:   wire.NormalizeStorageWord,
			Encode:      wire.EncodeStorageWord,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{2}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 2},
		},
		{
			Method:      MethodGetCode,
			Resolver:    ResolverStrict,
			Normalize:   wire.NormalizeHexData,
			Encode:      wire.EncodeData,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
		},
		{
			Method:      MethodCall,
			Resolver:    ResolverCallRevert,
			Normalize:   normalizeCallOutcome,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
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
			Method:      MethodGetLogs,
			Resolver:    ResolverStrict,
			Normalize:   normalizeLogs,
			Encode:      logobject.Encode,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}},
			Pending:     PendingSpec{Mode: PendingRefuse, Pos: 0},
			Validate:    func(_ context.Context, req rpc.Request) error { return validateLogRequest(req) },
			Admit:       admitLogs,
		},
		{
			Method:      MethodFeeHistory,
			Resolver:    ResolverStrict,
			Normalize:   normalizeFeeHistory,
			Encode:      feehistory.Encode,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareMethodSpecific,
			Reference:   ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{1}},
			Pending:     PendingSpec{Mode: PendingObserve, Pos: 1},
			Validate:    func(_ context.Context, req rpc.Request) error { return validateFeeHistoryRequest(req) },
			Admit:       admitFeeHistory,
		},
		{
			Method:      MethodGasPrice,
			Resolver:    ResolverMedian,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			CompareKind: rpc.RuleCompareQuantity,
		},
		{
			Method:      MethodMaxPriorityFeePerGas,
			Resolver:    ResolverMedian,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			CompareKind: rpc.RuleCompareQuantity,
		},
		{
			Method:      MethodBlobBaseFee,
			Resolver:    ResolverMedian,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			CompareKind: rpc.RuleCompareQuantity,
		},
		{
			Method:      MethodBlockNumber,
			Resolver:    ResolverMedian,
			Normalize:   wire.NormalizeWireQuantity,
			Encode:      wire.EncodeQuantity,
			CompareKind: rpc.RuleCompareQuantity,
		},
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
		{
			Method:      MethodPeerCount,
			Resolver:    ResolverLocal,
			Normalize:   wire.NormalizeHexQuantity,
			Encode:      wire.EncodeFoldedQuantity,
			CompareKind: rpc.RuleCompareQuantity,
		},
		{
			Method:      MethodSyncing,
			Resolver:    ResolverLocal,
			Normalize:   normalizeSyncing,
			Encode:      rpc.EncodeCanonicalJSON,
			CompareKind: rpc.RuleCompareObject,
		},
		{
			Method:      MethodNetVersion,
			Resolver:    ResolverStrict,
			Normalize:   wire.NormalizeDecimalString,
			Encode:      wire.EncodeDecimal,
			KeySource:   rpc.RuleKeyCanonicalBytes,
			CompareKind: rpc.RuleCompareQuantity,
		},
		{
			Method:      MethodListening,
			Resolver:    ResolverLocal,
			Normalize:   normalizeListening,
			Encode:      rpc.EncodeCanonicalJSON,
			CompareKind: rpc.RuleCompareObject,
		},
	}
}

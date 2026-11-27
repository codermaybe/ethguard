package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	receiptobject "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodGetBlockReceipts is the JSON-RPC method spelling this file owns: the
// whole-block receipt list read.
const MethodGetBlockReceipts = "eth_getBlockReceipts"

// getBlockReceiptsSpec is the method's compiled contract: canonical
// receipt-list comparison over the strict resolver, an explicit block
// reference, pending refused locally, and the list admission binding. No
// whole-key compatibility correction applies to this method.
func getBlockReceiptsSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGetBlockReceipts,
		Resolver:    ResolverStrict,
		Normalize:   normalizeBlockReceiptList,
		Encode:      receiptobject.EncodeBlock,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareMethodSpecific,
		Reference:   ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{0}, EIP1898: true},
		Pending:     PendingSpec{Mode: PendingRefuse, Pos: 0},
		Prepare: func(ctx context.Context, req rpc.Request) (rpc.Request, error) {
			return req, validateBlockReceiptsRequest(ctx, req)
		},
		Admit:                  admitBlockReceipts,
		ProcessCanonical:       processBlockReceipts,
		ObservationProjectorID: ProjectorBlockReceipts,
	}
}

func getBlockReceiptsDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetBlockReceipts,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "BlockReceipts",
		Alignment: "v1.17.10",
	}
}

func admitBlockReceipts(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := receiptobject.AdmitBlock(req, value); err != nil {
		return nil, err
	}
	return nil, nil
}

// processBlockReceipts reuses the canonical row bytes checked during admission.
// Safety admission has already checked the complete raw list. The original raw
// Admit/Encode route remains available to invocations without this processor.
func processBlockReceipts(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
	canonical, err := receiptobject.PrepareBlock(req, value)
	if err != nil {
		return rpc.CanonicalResult{}, err
	}
	return rpc.NewCanonicalResult(canonical, nil)
}

// normalizeBlockReceiptList is the single canonical pass: the pruner's
// output is already the sorted, compact, hex-canonical comparison key, and
// the committed value's encoder owns any further shaping. A second
// marshal here would be an independent owner that can only drift.
func normalizeBlockReceiptList(value json.RawMessage) (string, error) {
	return receiptobject.NormalizeBlock(value, wiremask.PruneMethod, MethodGetBlockReceipts)
}

// validateBlockReceiptsRequest owns the local rejections the
// eth_getBlockReceipts matrix row promises before any establishment or
// provider contact: requireCanonical combined with a moving tag is refused
// outright — never silently dropped, never resolved after fan-out — while
// explicit heights, hashes, pins, and canonical-free moving tags pass
// through to the reference machinery.
func validateBlockReceiptsRequest(_ context.Context, req rpc.Request) error {
	// Installing a validate hook suppresses the auto-installed pending
	// rejection (CompilePlan only adds it for methods without one), so this
	// hook owns the pending column's local semantics too: the same standard
	// spelling check PendingRejectAt would have run.
	if err := rpc.PendingRejectAt(0, MethodGetBlockReceipts)(req); err != nil {
		return err
	}
	if len(req.Params) != 1 {
		return fmt.Errorf("%w: eth_getBlockReceipts expects one block reference", rpc.ErrInvalidRequest)
	}
	obj, ok := req.Params[0].(map[string]any)
	if !ok {
		return nil
	}
	if canon, hasCanon := obj["requireCanonical"].(bool); hasCanon && canon {
		if number, hasNumber := obj["blockNumber"]; hasNumber {
			if tag, isStr := number.(string); isStr {
				switch strings.ToLower(tag) {
				case "latest", "safe", "finalized":
					return fmt.Errorf("%w: eth_getBlockReceipts requireCanonical cannot be combined with the moving tag %q", rpc.ErrInvalidRequest, tag)
				}
			}
		}
	}
	return nil
}

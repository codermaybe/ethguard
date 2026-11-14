package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
	receiptobject "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
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

func normalizeBlockReceiptList(value json.RawMessage) (string, error) {
	key, err := receiptobject.NormalizeBlock(value, wire.PruneMethod, MethodGetBlockReceipts)
	if err != nil || key == "null" {
		return key, err
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(key), &rows); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
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

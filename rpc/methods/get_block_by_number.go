package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
)

// MethodGetBlockByNumber is the JSON-RPC method spelling this file owns: the
// block read addressed by height or tag, and the moving-tag anchor the
// reference establishment rides.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// getBlockByNumberSpec is the method's compiled contract: block-object
// comparison under the block wrapper, pending observed at the height
// parameter, and the full-block admission binding. No whole-key
// compatibility correction applies to this method.
func getBlockByNumberSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetBlockByNumber,
		Resolver:               ResolverBlock,
		Normalize:              normalizeBlockIdentity,
		Encode:                 blockobject.EncodeByNumber,
		KeySource:              rpc.RuleKeyNormalizer,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Pending:                PendingSpec{Mode: PendingObserve, Pos: 0},
		Admit:                  admitBlockByNumberResponse,
		ObservationProjectorID: ProjectorBlock,
	}
}

func getBlockByNumberDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetBlockByNumber,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "BlockByNumber",
		Alignment: "v1.17.10",
	}
}

// admitBlockByNumberResponse binds the number-addressed block identity
// explicitly so plan and compatibility admission cannot drift through
// request-method inference.
func admitBlockByNumberResponse(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, false, call)
}

package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	blockobject "github.com/codermaybe/ethguard/internal/ethobject/block"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodGetBlockByNumber is the JSON-RPC method spelling this file owns: the
// block read addressed by height or tag, and the moving-tag anchor the
// reference establishment rides.
const MethodGetBlockByNumber = "eth_getBlockByNumber"

// getBlockByNumberSpec is the method's compiled contract: block-object
// strict comparison of canonical blocks, pending observed at the height
// parameter, and the full-block admission binding. No whole-key
// compatibility correction applies to this method.
func getBlockByNumberSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetBlockByNumber,
		Resolver:               ResolverStrict,
		Normalize:              normalizeBlockIdentity,
		Encode:                 blockobject.Encode,
		KeySource:              rpcengine.RuleKeyCanonicalBytes,
		CompareKind:            rpcengine.RuleCompareMethodSpecific,
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
// explicitly instead of inferring it from the request method.
func admitBlockByNumberResponse(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, false, call)
}

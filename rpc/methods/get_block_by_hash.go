package methods

import (
	"context"
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
)

// MethodGetBlockByHash is the JSON-RPC method spelling this file owns: the
// block read addressed by hash.
const MethodGetBlockByHash = "eth_getBlockByHash"

// getBlockByHashSpec is the method's compiled contract: block-object
// comparison under the block wrapper with the hash-addressed admission
// binding. No whole-key compatibility correction applies to this method.
func getBlockByHashSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetBlockByHash,
		Resolver:               ResolverBlock,
		ByHash:                 true,
		Normalize:              normalizeBlockIdentity,
		Encode:                 blockobject.Encode,
		KeySource:              rpc.RuleKeyCanonicalBytes,
		CompareKind:            rpc.RuleCompareMethodSpecific,
		Admit:                  admitBlockByHashResponse,
		ObservationProjectorID: ProjectorBlock,
	}
}

func getBlockByHashDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetBlockByHash,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "BlockByHash",
		Alignment: "v1.17.10",
	}
}

// admitBlockByHashResponse binds the hash-addressed block identity
// explicitly so plan and compatibility admission cannot drift through
// request-method inference.
func admitBlockByHashResponse(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, true, call)
}

package methods

import (
	"context"
	"encoding/json"

	blockobject "github.com/codermaybe/ethguard/internal/ethobject/block"
	"github.com/codermaybe/ethguard/rpc"
)

// MethodGetBlockByHash is the JSON-RPC method spelling this file owns: the
// block read addressed by hash.
const MethodGetBlockByHash = "eth_getBlockByHash"

// getBlockByHashSpec is the method's compiled contract: block-object
// strict comparison of canonical blocks with the hash-addressed admission
// binding. No whole-key compatibility correction applies to this method.
func getBlockByHashSpec() MethodSpec {
	return MethodSpec{
		Method:                 MethodGetBlockByHash,
		Resolver:               ResolverStrict,
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
// explicitly instead of inferring it from the request method.
func admitBlockByHashResponse(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	return admitBlockResponse(ctx, req, value, true, call)
}

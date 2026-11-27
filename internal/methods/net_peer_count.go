package methods

import (
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/rpcengine"
)

// MethodPeerCount is the JSON-RPC method spelling this file owns. Note the
// namespace: this is a net_ method, corroborated like any other registered
// method.
const MethodPeerCount = "net_peerCount"

// peerCountSpec is the method's compiled contract: folded-quantity sampling
// over the local resolver; the value is node-local, so sources are sampled
// without clustering. No whole-key compatibility correction applies to this
// method.
func peerCountSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodPeerCount,
		Resolver:    ResolverLocal,
		Normalize:   standardQuantityResult(ethapi.NewPeerCountResult, ethcodec.FoldPeerQuantity),
		Encode:      standardQuantityEncoding(ethapi.NewPeerCountResult, ethcodec.FoldPeerQuantity),
		CompareKind: rpcengine.RuleCompareQuantity,
	}
}

func peerCountDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodPeerCount,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "PeerCount",
		Alignment: "v1.17.10",
	}
}

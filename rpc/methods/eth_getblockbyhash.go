package methods

import (
	"ethguard/rpc"
	blockobject "ethguard/rpc/internal/objects/block"
)

// MethodGetBlockByHash is the JSON-RPC method name resolved by
// BlockByHashPolicy.
const MethodGetBlockByHash = "eth_getBlockByHash"

// BlockByHashPolicy corroborates eth_getBlockByHash by full agreement. The block
// hash addresses immutable content, so comparison rides the shared
// canonical-object normalizer: encoding differences are erased, everything
// still differing between sources is dissent.
func BlockByHashPolicy(opts ...rpc.StrictOption) rpc.Policy {
	strictOpts := append([]rpc.StrictOption{rpc.WithCanonicalObject(blockobject.Encode)}, opts...)
	return blockPolicy{Policy: rpc.Strict(normalizeBlockIdentity, compareOpts(rpc.RuleCompareMethodSpecific, strictOpts)...), byHash: true}
}

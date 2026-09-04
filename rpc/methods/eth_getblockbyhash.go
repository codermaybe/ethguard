package methods

import (
	"ethguard/rpc"
)

// MethodGetBlockByHash is the JSON-RPC method name resolved by
// BlockByHashPolicy.
const MethodGetBlockByHash = "eth_getBlockByHash"

// BlockByHashPolicy verifies eth_getBlockByHash by full agreement. The block
// hash addresses immutable content, so comparison rides the shared
// canonical-object normalizer: encoding differences are erased, everything
// still differing between sources is dissent.
func BlockByHashPolicy() rpc.Policy { return rpc.Strict(normalizeCanonicalObject) }

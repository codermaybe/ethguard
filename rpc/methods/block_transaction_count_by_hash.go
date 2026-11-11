package methods

import (
	"encoding/json"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodGetBlockTransactionCountByHash is the JSON-RPC method spelling this
// file owns. It counts transactions in one immutable block and is distinct
// from eth_getTransactionCount, which returns an account nonce.
const MethodGetBlockTransactionCountByHash = "eth_getBlockTransactionCountByHash"

// blockTransactionCountByHashSpec is the method's compiled contract:
// quantity comparison over the strict resolver. No whole-key compatibility
// correction applies to this method.
func blockTransactionCountByHashSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodGetBlockTransactionCountByHash,
		Resolver:    ResolverStrict,
		Normalize:   normalizeTransactionCount,
		Encode:      encodeTransactionCount,
		KeySource:   rpc.RuleKeyNormalizer,
		CompareKind: rpc.RuleCompareQuantity,
	}
}

func blockTransactionCountByHashDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodGetBlockTransactionCountByHash,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "TransactionCount",
		Alignment: "v1.17.10",
	}
}

// normalizeTransactionCount admits the protocol's null absence so strict
// resolution can preserve it for the typed boundary, while applying the
// canonical wire quantity grammar to actual counts.
func normalizeTransactionCount(value json.RawMessage) (string, error) {
	if wire.IsJSONNull(value) {
		return "null", nil
	}
	return wire.NormalizeWireQuantity(value)
}

func encodeTransactionCount(value json.RawMessage) (json.RawMessage, error) {
	if wire.IsJSONNull(value) {
		return json.RawMessage("null"), nil
	}
	return wire.EncodeQuantity(value)
}

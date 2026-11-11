package methods

import (
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// MethodNetVersion is the JSON-RPC method spelling this file owns: the
// decimal network identity behind NetworkID.
const MethodNetVersion = "net_version"

// netVersionSpec is the method's compiled contract: decimal identity over
// the strict resolver. No whole-key compatibility correction applies to this
// method.
func netVersionSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodNetVersion,
		Resolver:    ResolverStrict,
		Normalize:   wire.NormalizeDecimalString,
		Encode:      wire.EncodeDecimal,
		KeySource:   rpc.RuleKeyCanonicalBytes,
		CompareKind: rpc.RuleCompareQuantity,
	}
}

func netVersionDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodNetVersion,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Adapter:   "NetworkID",
		Alignment: "v1.17.10",
		Notes:     "Root Client.NetworkID (net_version).",
	}
}

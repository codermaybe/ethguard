package methods

import (
	"encoding/json"
	"github.com/codermaybe/ethguard/ethapi"
	"strconv"

	"github.com/codermaybe/ethguard/internal/rpcengine"
)

// MethodListening is the JSON-RPC method spelling this file owns: the root
// extension behind the Call-only read.
const MethodListening = "net_listening"

// listeningSpec is the method's compiled contract: boolean-object sampling
// over the local resolver; the value is node-local, so sources are sampled
// without clustering. No whole-key compatibility correction applies to this
// method.
func listeningSpec() MethodSpec {
	return MethodSpec{
		Method:      MethodListening,
		Resolver:    ResolverLocal,
		Normalize:   normalizeListening,
		Encode:      encodeListening,
		CompareKind: rpcengine.RuleCompareObject,
	}
}

func listeningDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodListening,
		Kind:      RegisteredMethod,
		Family:    FamilyRead,
		Coverage:  CoverageRegistered,
		Policy:    "CompilePlan",
		Alignment: "v1.17.10",
		Notes:     "No typed method; the full decision comes from Client.Call(\"net_listening\").",
	}
}

// normalizeListening admits only the method's boolean result. JSON null and
// non-boolean bodies are malformed, not a negative listening state. The
// registered plan resolves net_listening through rpc.Local with this
// normalizer and the generated official boolean result constructor.
func normalizeListening(value json.RawMessage) (string, error) {
	parsed, err := ethapi.NewListeningResult(value)
	if err != nil {
		return "", err
	}
	return strconv.FormatBool(parsed), nil
}

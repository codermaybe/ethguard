package methods

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
)

// MethodListening is the JSON-RPC method name resolved by ListeningPolicy.
const MethodListening = "net_listening"

// ListeningPolicy resolves net_listening as an upstream-local result: whether
// a node accepts inbound peers describes one provider alone (see rpc.Local).
func ListeningPolicy() rpc.Policy {
	return rpc.Local(normalizeListening, rpc.WithLocalRuleCompareKind(rpc.RuleCompareObject), rpc.WithLocalCanonical(rpc.EncodeCanonicalJSON))
}

// normalizeListening admits only the method's boolean result. JSON null and
// non-boolean bodies are malformed, not a negative listening state.
func normalizeListening(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) {
		return string(trimmed), nil
	}
	return "", fmt.Errorf("listening: expected a boolean")
}

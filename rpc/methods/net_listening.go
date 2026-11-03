package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// normalizeListening admits only the method's boolean result. JSON null and
// non-boolean bodies are malformed, not a negative listening state. The
// registered plan resolves net_listening through rpc.Local with this
// normalizer; the method has no constructor.
func normalizeListening(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) {
		return string(trimmed), nil
	}
	return "", fmt.Errorf("listening: expected a boolean")
}

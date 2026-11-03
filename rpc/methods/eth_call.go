package methods

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// normalizeCallOutcome reduces one eth_call answer to a comparable key.
// It canonicalizes return data and revert data. It does not admit the call:
// a payload with no canonical form errors, and a non-revert protocol error
// errors so it stays a fault. Method admission is not this function's job.
func normalizeCallOutcome(value json.RawMessage) (string, error) {
	var result string
	if err := json.Unmarshal(value, &result); err == nil {
		data, err := wire.NormalizeHexData(value)
		if err != nil {
			return "", err
		}
		return "ok|" + data, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		return "", fmt.Errorf("call outcome: unrecognized payload")
	}
	var code int
	var message string
	if raw, ok := fields["code"]; ok {
		if err := json.Unmarshal(raw, &code); err != nil {
			return "", fmt.Errorf("call outcome: unrecognized payload")
		}
	}
	if raw, ok := fields["message"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &message); err != nil {
			return "", fmt.Errorf("call outcome: unrecognized payload")
		}
	}
	if code == 0 && message == "" {
		return "", fmt.Errorf("call outcome: unrecognized payload")
	}
	state, err := revertDataState(fields)
	if err != nil {
		return "", err
	}
	if isRevertObservation(code, message) || bareHexRevert(code, message, state) {
		return "reverted|" + state, nil
	}
	return "", fmt.Errorf("call outcome: protocol error %d is not an execution observation", code)
}

// encodeCallSuccess canonicalizes a successful eth_call result. The returned
// JSON is the execution key and the committed Value. Revert envelopes return
// ErrNotCanonicalScalar, keep the reverted| key, and do not enter Value as
// canonical DATA. The ok| prefix is not part of the returned JSON string.
func encodeCallSuccess(value json.RawMessage) (json.RawMessage, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return nil, rpc.ErrNotCanonicalScalar
	}
	return wire.EncodeData(value)
}

// revertDataState is the data half of a revert key. Missing, JSON null,
// empty bytes ("0x" in either hex-prefix case), other even-length hex, and
// non-hex JSON each take a different state. An empty JSON string is not
// empty bytes.
func revertDataState(fields map[string]json.RawMessage) (string, error) {
	raw, ok := fields["data"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 {
		return "missing", nil
	}
	trimmed := bytes.TrimSpace(raw)
	if string(trimmed) == "null" {
		return "null", nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if strings.EqualFold(text, "0x") {
			return "empty", nil
		}
		if canonical, ok := canonicalHexData(text); ok {
			return "hex:" + canonical, nil
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return "", err
		}
		return "raw:" + string(encoded), nil
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return "", fmt.Errorf("call outcome: revert data is not JSON: %w", err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	return "raw:" + string(encoded), nil
}

func canonicalHexData(text string) (string, bool) {
	if len(text) < 4 || !strings.EqualFold(text[:2], "0x") {
		return "", false
	}
	digits := text[2:]
	if len(digits)%2 != 0 {
		return "", false
	}
	if _, err := hex.DecodeString(digits); err != nil {
		return "", false
	}
	return "0x" + strings.ToLower(digits), true
}

// bareHexRevert keeps the historical empty-message -32000 shape, but only
// when data is hex. A non-hex body is not an execution observation.
func bareHexRevert(code int, message, state string) bool {
	return code == -32000 && message == "" && (state == "empty" || strings.HasPrefix(state, "hex:"))
}

// isRevertObservation classifies revert-class JSON-RPC errors. The geth
// family answers reverts with -32000 and a revert message; legacy parity
// used code 3. Everything else — including geth's own client-side failure
// codes — is a fault, not an execution observation, and the live soak
// refines the boundary as provider families show their shapes.
func isRevertObservation(code int, message string) bool {
	if code == 3 {
		return true
	}
	if code != -32000 {
		return false
	}
	msg := strings.ToLower(message)
	return strings.Contains(msg, "execution reverted") || strings.Contains(msg, "revert")
}

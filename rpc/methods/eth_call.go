package methods

import (
	"encoding/json"
	"fmt"
	"strings"

	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// MethodCall is the JSON-RPC method name resolved by CallPolicy.
const MethodCall = "eth_call"

// CallPolicy corroborates eth_call as behavior, not content (ruling 2026-09-04):
// unanimous execution failure is a corroborated observation, a split between
// results and reverts is conflict, and the content inside a revert stays out
// of scope. Concretely, the normalizer collapses every revert-class error to
// one key, so benign client-to-client differences in revert wording cannot
// fake dissent, while return data rides the hex-data normalizer. Provider
// failures that are not execution observations — transport faults, geth's
// own client-side errors (-32002 timeout, -32003 response too large,
// -32603 panic), rate limits — ride as faults, never as corroborated outcomes.
// The block tag at position 1 is rewritten to the corroborated reference.
// EIP-1898 object forms ride EIP1898BlockRef (see GetBalancePolicy).
// Pending resolves as a node-local observation (see GetBalancePolicy).
func CallPolicy(anchor rpc.AnchorFunc, opts ...rpc.StrictOption) rpc.Policy {
	return PendingLocal(1)(EIP1898BlockRef(anchor, 1)(rpc.Anchored(1, normalizeCallOutcome, anchor,
		append([]rpc.StrictOption{rpc.WithRPCErrorBodies()}, opts...)...)))
}

// normalizeCallOutcome reduces one eth_call answer to a comparable key.
// Successful return data is compared as hex data; a revert-class protocol
// error collapses to the constant "reverted" — the corroborated observation is
// that the call reverted, not the wording of any particular revert message.
func normalizeCallOutcome(value json.RawMessage) (string, error) {
	var result string
	if err := json.Unmarshal(value, &result); err == nil {
		data, err := wire.NormalizeHexData(value)
		if err != nil {
			return "", err
		}
		return "ok|" + data, nil
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(value, &envelope); err != nil || (envelope.Code == 0 && envelope.Message == "") {
		return "", fmt.Errorf("call outcome: unrecognized payload")
	}
	if isRevertObservation(envelope.Code, envelope.Message) ||
		(envelope.Code == -32000 && envelope.Message == "" && strings.HasPrefix(envelope.Data, "0x")) {
		return "reverted", nil
	}
	return "", fmt.Errorf("call outcome: protocol error %d is not an execution observation", envelope.Code)
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

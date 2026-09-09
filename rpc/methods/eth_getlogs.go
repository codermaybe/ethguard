package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"ethguard/rpc"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// MethodGetLogs is the JSON-RPC method name resolved by LogsPolicy.
const MethodGetLogs = "eth_getLogs"

// LogsPolicy verifies eth_getLogs by full agreement over the whole log
// array. Once the range is pinned the response is chain-determined and
// strictly comparable (analysis 2026-09-04): every log is ordered by its
// chain-assigned index, so benign encoding differences are erased by the
// canonical-object normalizer and whatever still differs is dissent.
//
// The range lives in the filter object's fromBlock/toBlock fields, and the
// wire types those fields as BlockNumbers — a 32-byte hash dies there with
// "hex number > 64 bits" (the soak's first catch; blockHash is the
// dedicated, mutually exclusive hash position). The bounds are therefore
// pinned to the verified reference's height: moving tags are rewritten and
// absent fields are defaulted, since a provider's implicit "latest" is a
// moving tag in field clothing. blockHash pins pass through untouched.
//
// Payload cost makes getLogs a singleflight candidate (deferred);
// pruned-node gaps surface as dissent and fail closed — excluding stale
// reporters stays a deferred liveness knob, the same shape as decision
// 0016.
func LogsPolicy(height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	inner := logsPolicy{Policy: rpc.Strict(normalizeLogs, opts...)}
	return rpc.WithAnchorHeightFields(height, 0, "fromBlock", "toBlock")(inner)
}

// logsPolicy validates the filter before anchoring and normalizes a protocol
// projection of each log. Provider-added fields therefore do not create a
// false disagreement, while ordering and duplicates remain significant.
type logsPolicy struct{ rpc.Policy }

func (p logsPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	if err := validateLogRequest(req); err != nil {
		return rpc.Request{}, err
	}
	return p.Policy.Prepare(ctx, req)
}
func (p logsPolicy) Normalize(value json.RawMessage) (string, error) {
	return normalizeLogs(value)
}
func (p logsPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.Resolve(obs)
}
func (p logsPolicy) RequiresAgreement() bool { return true }
func (p logsPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if policy, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return policy.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}

// Admit enforces the log response schema before the response can enter a
// voting cluster.  The normalizer deliberately runs later as well: admission
// protects the security boundary, while normalization only chooses the
// canonical representation used for clustering.
func (p logsPolicy) Admit(_ context.Context, req rpc.Request, value json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
	if err := validateLogsResponse(req, value); err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil, nil
}

func validateLogRequest(req rpc.Request) error {
	if len(req.Params) != 1 {
		return fmt.Errorf("%w: eth_getLogs expects one filter", rpc.ErrInvalidRequest)
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		// Keep the policy usable by the generic anchor tests and callers that
		// perform their own typed decoding. The adapter supplies an object and
		// receives the full filter validation below.
		return nil
	}
	if _, hasHash := filter["blockHash"]; hasHash {
		if _, hasFrom := filter["fromBlock"]; hasFrom {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with fromBlock", rpc.ErrInvalidRequest)
		}
		if _, hasTo := filter["toBlock"]; hasTo {
			return fmt.Errorf("%w: eth_getLogs blockHash conflicts with toBlock", rpc.ErrInvalidRequest)
		}
	}
	return nil
}

func normalizeLogs(value json.RawMessage) (string, error) {
	value = bytes.TrimSpace(value)
	var rawLogs []json.RawMessage
	if err := json.Unmarshal(value, &rawLogs); err != nil || string(value) == "null" {
		return "", fmt.Errorf("logs: expected an array")
	}
	projection := make([]json.RawMessage, len(rawLogs))
	for i, raw := range rawLogs {
		obj, err := normalizedLogObject(raw)
		if err != nil {
			return "", fmt.Errorf("logs[%d]: %w", i, err)
		}
		encoded, err := json.Marshal(obj)
		if err != nil {
			return "", err
		}
		projection[i] = encoded
	}
	encoded, err := json.Marshal(projection)
	return string(encoded), err
}

// validateLogsResponse is intentionally stricter than types.Log's JSON
// decoder.  The decoder assigns zero values to omitted fields, which would
// allow an incomplete response to become a valid vote.
func validateLogsResponse(req rpc.Request, value json.RawMessage) error {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("logs: expected an array")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
		return fmt.Errorf("logs: expected an array")
	}
	for i, row := range rows {
		if _, err := normalizedLogObject(row); err != nil {
			return fmt.Errorf("logs[%d]: %w", i, err)
		}
	}
	return validateLogRequestBinding(req, rows)
}

func normalizedLogObject(value json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("expected an object")
	}
	for _, field := range []string{"address", "topics", "data", "blockNumber", "transactionHash", "transactionIndex", "blockHash", "logIndex", "removed"} {
		if raw, ok := obj[field]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("missing %s", field)
		}
	}
	if err := strictAddress(obj["address"]); err != nil {
		return nil, fmt.Errorf("address: %w", err)
	}
	var topics []json.RawMessage
	if err := json.Unmarshal(obj["topics"], &topics); err != nil || topics == nil {
		return nil, fmt.Errorf("topics must be an array")
	}
	for i, topic := range topics {
		if err := strictHash(topic); err != nil {
			return nil, fmt.Errorf("topics[%d]: %w", i, err)
		}
	}
	if err := strictData(obj["data"]); err != nil {
		return nil, fmt.Errorf("data: %w", err)
	}
	for _, field := range []string{"blockNumber", "transactionIndex", "logIndex"} {
		if err := strictQuantity(obj[field]); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
	}
	for _, field := range []string{"transactionHash", "blockHash"} {
		if err := strictHash(obj[field]); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
	}
	var removed bool
	if err := json.Unmarshal(obj["removed"], &removed); err != nil {
		return nil, fmt.Errorf("removed must be boolean")
	}
	return obj, nil
}

func validateLogRequestBinding(req rpc.Request, rows []json.RawMessage) error {
	if len(req.Params) != 1 {
		return nil
	}
	filter, ok := req.Params[0].(map[string]any)
	if !ok {
		return nil
	}
	if requested, ok := filter["blockHash"]; ok {
		want, ok := valueHash(requested)
		if !ok {
			return fmt.Errorf("blockHash filter must be a 32-byte hash")
		}
		for i, row := range rows {
			var obj map[string]json.RawMessage
			_ = json.Unmarshal(row, &obj)
			var got common.Hash
			if err := json.Unmarshal(obj["blockHash"], &got); err != nil || got != want {
				return fmt.Errorf("log %d blockHash does not match requested blockHash", i)
			}
		}
	}
	return nil
}

func strictAddress(value json.RawMessage) error {
	var s string
	if err := json.Unmarshal(value, &s); err != nil || !common.IsHexAddress(s) || len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return fmt.Errorf("expected 20-byte 0x-prefixed address")
	}
	return nil
}

func strictHash(value json.RawMessage) error {
	var s string
	if err := json.Unmarshal(value, &s); err != nil || len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return fmt.Errorf("expected 32-byte 0x-prefixed hash")
	}
	if !common.IsHexHash(s) {
		return fmt.Errorf("invalid hash")
	}
	return nil
}

func strictData(value json.RawMessage) error {
	var s string
	if err := json.Unmarshal(value, &s); err != nil || len(s) < 2 || !strings.HasPrefix(s, "0x") || len(s[2:])%2 != 0 {
		return fmt.Errorf("expected even-length 0x-prefixed data")
	}
	for _, r := range s[2:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return fmt.Errorf("invalid data")
		}
	}
	return nil
}

func strictQuantity(value json.RawMessage) error {
	var s string
	if err := json.Unmarshal(value, &s); err != nil || len(s) < 3 || !strings.HasPrefix(s, "0x") || len(s) == 2 {
		return fmt.Errorf("expected non-empty 0x-prefixed quantity")
	}
	if _, ok := new(big.Int).SetString(s[2:], 16); !ok {
		return fmt.Errorf("invalid quantity")
	}
	return nil
}

func valueHash(value any) (common.Hash, bool) {
	switch v := value.(type) {
	case common.Hash:
		return v, true
	case string:
		if len(v) != 66 || !common.IsHexHash(v) {
			return common.Hash{}, false
		}
		return common.HexToHash(v), true
	default:
		return common.Hash{}, false
	}
}

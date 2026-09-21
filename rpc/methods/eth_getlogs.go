package methods

import (
	"context"
	"encoding/json"
	"ethguard/rpc"
	"fmt"
)

// MethodGetLogs is the JSON-RPC method name resolved by LogsPolicy.
const MethodGetLogs = "eth_getLogs"

// LogsPolicy corroborates eth_getLogs by full agreement over the whole log
// array. Once the range is pinned the response is chain-determined and
// strictly comparable (analysis 2026-09-04): every log is ordered by its
// chain-assigned index, so the spec whitelist erases benign encoding and
// vendor differences and whatever still differs is dissent.
//
// The range lives in the filter object's fromBlock/toBlock fields, and the
// wire types those fields as BlockNumbers — a 32-byte hash dies there with
// "hex number > 64 bits" (the soak's first catch; blockHash is the
// dedicated, mutually exclusive hash position). The bounds are therefore
// pinned to the corroborated reference's height: moving tags are rewritten and
// absent fields are defaulted, since a provider's implicit "latest" is a
// moving tag in field clothing. blockHash pins pass through untouched.
// pending bounds are rejected locally: a log range over node-local pending
// state cannot claim Corroborated semantics, and this policy has no local
// observation projection for a mixed filter.
//
// Payload cost makes getLogs a singleflight candidate (deferred);
// pruned-node gaps surface as dissent and fail closed — excluding stale
// reporters stays a deferred liveness knob, the same shape as decision
// 0016.
func LogsPolicy(height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	inner := logsPolicy{Policy: rpc.Strict(normalizeLogs, opts...)}
	return rpc.WithAnchorHeightFields(height, 0, "fromBlock", "toBlock")(inner)
}

// logsPolicy is the thin assembly for eth_getLogs: request validation before
// anchoring lives here, while Log validation, projection, and helpers live in
// logs.go (the Log object slice).
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

package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/log"
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
// moving tag in field clothing. A real blockHash pin passes through untouched.
// The pending tag in blockHash is not a hash and is rejected locally.
// pending bounds are rejected in Prepare before any anchor lookup: a log
// range over node-local pending state has no stable corroboration semantics,
// and this policy has no field-level pending observation. That rejection is
// ErrInvalidRequest, not a provider failure and not a business Unknown
// adjudication. Corroborated remains provider-backed cross-source evidence.
//
// Payload cost makes getLogs a singleflight candidate (deferred);
// pruned-node gaps surface as dissent and fail closed — excluding stale
// reporters stays a deferred liveness knob, the same shape as decision
// 0016.
func LogsPolicy(height rpc.AnchorHeightFunc, opts ...rpc.StrictOption) rpc.Policy {
	// Validation wraps anchoring so a pending or malformed bound returns
	// before WithAnchorHeightFields resolves a height.
	anchored := rpc.WithAnchorHeightFields(height, 0, "fromBlock", "toBlock")(rpc.Strict(normalizeLogs, compareOpts(rpc.RuleCompareMethodSpecific, append([]rpc.StrictOption{rpc.WithCanonicalValue(log.Encode)}, opts...))...))
	return logsPolicy{Policy: anchored}
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

func (p logsPolicy) BlockRef(original, prepared rpc.Request) string {
	if provider, ok := p.Policy.(rpc.BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (p logsPolicy) IncludeRPCError() bool {
	provider, ok := p.Policy.(rpc.RPCErrorPolicy)
	return ok && provider.IncludeRPCError()
}
func (p logsPolicy) Normalize(value json.RawMessage) (string, error) {
	return normalizeLogs(value)
}

// MethodRule reports eth_getLogs pending rejection. Prepare returns
// ErrInvalidRequest before any anchor lookup or upstream call. This method
// does not change that path.
func (p logsPolicy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	rule := rpc.MethodRule{Pending: rpc.RulePendingLocalReject}
	if inner, ok := p.Policy.(rpc.MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, p.Policy
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

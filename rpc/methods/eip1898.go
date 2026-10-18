package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/rpc"
)

// EIP1898BlockRef decorates a string-anchored policy (built with rpc.Anchored
// or rpc.WithAnchor) with EIP-1898 object-form handling at parameter pos.
//
// Why this layer exists: the five default-block state reads (eth_getBalance,
// eth_getTransactionCount, eth_getStorageAt, eth_getCode, eth_call) accept
// the full EIP-1898 BlockNumberOrHash — a hex quantity, a tag string, or an
// object {blockHash} / {blockNumber[, requireCanonical]}. The shared anchor
// layer only understands string positions: object inputs used to slip past
// anchoring unrewritten (a {blockNumber: "latest"} object fanned out as-is
// turns the read into inevitable dissent), contradictory objects reached
// providers instead of failing locally, and requireCanonical survived only
// by accident with no test pinning it.
//
// Contract at pos:
//
//   - string: delegated to the inner anchored policy unchanged (moving tags
//     rewrite to the bare anchor hash, explicit numbers/hashes and pending
//     pass through). The bare-hash rewrite is provider-accepted but
//     non-standard; it is kept because the soak verified it, not because
//     the standard blesses it.
//   - {blockHash[, requireCanonical]}: explicit pin, passes through
//     untouched. No anchor call; requireCanonical is preserved.
//   - {blockNumber: quantity-or-earliest[, requireCanonical]}: passes
//     through untouched, requireCanonical preserved.
//   - {blockNumber: movingTag[, requireCanonical]}: tag-aware anchor
//     rewrite to {blockHash: anchor[, requireCanonical]}. The tag is never
//     downgraded (safe/finalized keep their level via the tag-aware
//     resolver) and requireCanonical is never dropped.
//   - {blockNumber: "pending"}: passes through untouched, like the string
//     form. Pending keeps its node-state meaning; it is never rewritten
//     into a mined-state read.
//   - {blockHash, blockNumber} together, empty objects, malformed hashes,
//     non-string blockNumber/blockHash, non-boolean requireCanonical, and
//     unknown keys: rejected locally with ErrInvalidRequest before any
//     upstream contact.
//
// This decorator is applied per method, only where the official request
// shape allows objects. Methods whose wire position takes a bare quantity
// only (eth_feeHistory newestBlock, eth_getLogs fromBlock/toBlock,
// eth_getBlockByNumber number) must never receive this treatment: a hash
// cannot ride those positions.
func EIP1898BlockRef(anchor rpc.AnchorFunc, pos int) func(rpc.Policy) rpc.Policy {
	return func(policy rpc.Policy) rpc.Policy {
		if policy == nil {
			policy = rpc.ForwardPolicy{}
		}
		base := eip1898Policy{policy: policy, anchor: anchor, pos: pos}
		if _, ok := policy.(rpc.CorroboratingPolicy); ok {
			return corroboratingEIP1898Policy{base}
		}
		return base
	}
}

type eip1898Policy struct {
	policy rpc.Policy
	anchor rpc.AnchorFunc
	pos    int
}

func (p eip1898Policy) Admit(ctx context.Context, req rpc.Request, value json.RawMessage, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if inner, ok := p.policy.(rpc.AdmissionPolicy); ok {
		return inner.Admit(ctx, req, value, call)
	}
	return nil, nil
}

func (p eip1898Policy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	if p.pos < 0 || p.pos >= len(req.Params) {
		return p.policy.Prepare(ctx, req)
	}
	obj, ok := req.Params[p.pos].(map[string]any)
	if !ok {
		return p.policy.Prepare(ctx, req) // strings and anything else: inner owns them
	}
	pinned, needAnchor, tag, err := validateEIP1898Object(req.Method, obj)
	if err != nil {
		return rpc.Request{}, err
	}
	if !needAnchor {
		return p.policy.Prepare(ctx, req) // explicit or pending pin: complete, untouched
	}
	if p.anchor == nil {
		return rpc.Request{}, fmt.Errorf("anchor: missing anchor function")
	}
	ref, err := p.anchor(rpc.WithAnchorTag(ctx, tag))
	if err != nil {
		return rpc.Request{}, fmt.Errorf("anchor: %w", err)
	}
	out := map[string]any{"blockHash": ref}
	if pinned {
		out["requireCanonical"] = true
	}
	params := append([]any(nil), req.Params...)
	params[p.pos] = out
	return p.policy.Prepare(ctx, rpc.Request{Method: req.Method, Params: params})
}

// validateEIP1898Object checks one EIP-1898 BlockNumberOrHash object.
// It returns whether requireCanonical was set, whether the object carries a
// moving tag that still needs anchoring, and the tag itself.
func validateEIP1898Object(method string, obj map[string]any) (pinned, needAnchor bool, tag string, err error) {
	invalid := func(format string, args ...any) (bool, bool, string, error) {
		return false, false, "", fmt.Errorf("rpc: %s: %s: %w", method, fmt.Sprintf(format, args...), rpc.ErrInvalidRequest)
	}
	var (
		hash, hasHash     = obj["blockHash"]
		number, hasNumber = obj["blockNumber"]
		canon, hasCanon   = obj["requireCanonical"]
	)
	for key := range obj {
		switch key {
		case "blockHash", "blockNumber", "requireCanonical":
		default:
			return invalid("unknown field %q", key)
		}
	}
	if hasHash && hasNumber {
		return invalid("mixes blockHash with blockNumber")
	}
	if !hasHash && !hasNumber {
		return invalid("requires blockHash or blockNumber")
	}
	if hasCanon {
		canonBool, ok := canon.(bool)
		if !ok {
			return invalid("requireCanonical must be a boolean")
		}
		pinned = canonBool
	}
	if hasHash {
		hashStr, ok := hash.(string)
		if !ok || !isBlockHash(hashStr) {
			return invalid("blockHash must be a 32-byte hash")
		}
		return pinned, false, "", nil
	}
	numberStr, ok := number.(string)
	if !ok {
		return invalid("blockNumber must be a tag or hex quantity string")
	}
	lowered := strings.ToLower(numberStr)
	switch lowered {
	case "latest", "safe", "finalized":
		return pinned, true, lowered, nil
	case "pending", "earliest":
		return pinned, false, "", nil
	}
	if _, err := rpc.ParseHexQuantity(numberStr); err != nil {
		return invalid("blockNumber %q is not a tag or hex quantity", numberStr)
	}
	return pinned, false, "", nil
}

func isBlockHash(s string) bool {
	if len(s) != 2+64 || !strings.HasPrefix(s, "0x") {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// BlockRef reports the pinned hash when this request rewrote a
// {blockNumber: movingTag} object. String-form rewrites are reported by the
// inner anchored policy.
func (p eip1898Policy) BlockRef(original, prepared rpc.Request) string {
	if p.pos >= 0 && p.pos < len(original.Params) && p.pos < len(prepared.Params) {
		if originalObj, ok := original.Params[p.pos].(map[string]any); ok {
			if preparedObj, ok := prepared.Params[p.pos].(map[string]any); ok {
				if number, ok := originalObj["blockNumber"].(string); ok && isMovingTag(number) {
					if hash, ok := preparedObj["blockHash"].(string); ok {
						return hash
					}
				}
			}
		}
	}
	if provider, ok := p.policy.(rpc.BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func isMovingTag(s string) bool {
	switch strings.ToLower(s) {
	case "latest", "safe", "finalized":
		return true
	}
	return false
}

func (p eip1898Policy) RequiresAgreement() bool {
	inner, ok := p.policy.(rpc.AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

func (p eip1898Policy) SingleSource() bool {
	inner, ok := p.policy.(rpc.SingleSourcePolicy)
	return ok && inner.SingleSource()
}

func (p eip1898Policy) IncludeRPCError() bool {
	provider, ok := p.policy.(rpc.RPCErrorPolicy)
	return ok && provider.IncludeRPCError()
}

func (p eip1898Policy) Normalize(value json.RawMessage) (string, error) {
	return p.policy.Normalize(value)
}

// MethodRule reports the EIP-1898 block-reference form. It does not rewrite
// a request. The corroborating wrapper promotes this method.
func (p eip1898Policy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	rule := rpc.MethodRule{BlockRef: rpc.RuleBlockRefEIP1898}
	if inner, ok := p.policy.(rpc.MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, p.policy
}

func (p eip1898Policy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return p.policy.Resolve(obs)
}

type corroboratingEIP1898Policy struct{ eip1898Policy }

func (p corroboratingEIP1898Policy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if inner, ok := p.policy.(rpc.CorroboratingPolicy); ok {
		return inner.ResolveCorroborated(c, obs)
	}
	return p.policy.Resolve(obs)
}

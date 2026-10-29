package methods

import (
	"context"
	"fmt"

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
		base := eip1898Policy{Decorator: rpc.Decorator{Policy: policy}, anchor: anchor, pos: pos}
		if _, ok := policy.(rpc.CorroboratingPolicy); ok {
			return corroboratingEIP1898Policy{base}
		}
		return base
	}
}

type eip1898Policy struct {
	rpc.Decorator
	anchor rpc.AnchorFunc
	pos    int
}

func (p eip1898Policy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	if p.pos < 0 || p.pos >= len(req.Params) {
		return p.Policy.Prepare(ctx, req)
	}
	obj, ok := req.Params[p.pos].(map[string]any)
	if !ok {
		return p.Policy.Prepare(ctx, req) // strings and anything else: inner owns them
	}
	pinned, needAnchor, tag, err := validateEIP1898Object(req.Method, obj)
	if err != nil {
		return rpc.Request{}, err
	}
	if !needAnchor {
		return p.Policy.Prepare(ctx, req) // explicit or pending pin: complete, untouched
	}
	if p.anchor == nil {
		return rpc.Request{}, fmt.Errorf("anchor: missing anchor function")
	}
	ref, err := p.anchor(rpc.WithAnchorTag(ctx, tag))
	if err != nil {
		return rpc.Request{}, fmt.Errorf("anchor: %w", err)
	}
	out := rpc.EIP1898Rewrite(ref, pinned)
	params := append([]any(nil), req.Params...)
	params[p.pos] = out
	return p.Policy.Prepare(ctx, rpc.Request{Method: req.Method, Params: params})
}

// validateEIP1898Object checks one EIP-1898 BlockNumberOrHash object. It is
// the shared rpc implementation: the same validation serves the
// compatibility decorator and a planned read, so the object form's contract
// cannot drift between the two surfaces.
//
// It returns whether requireCanonical was set, whether the object carries a
// moving tag that still needs anchoring, and the tag itself.
func validateEIP1898Object(method string, obj map[string]any) (pinned, needAnchor bool, tag string, err error) {
	return rpc.ValidateEIP1898Object(method, obj)
}

// BlockRef reports the pinned hash when this request rewrote a
// {blockNumber: movingTag} object. String-form rewrites are reported by the
// inner anchored policy.
func (p eip1898Policy) BlockRef(original, prepared rpc.Request) string {
	if p.pos >= 0 && p.pos < len(original.Params) && p.pos < len(prepared.Params) {
		if originalObj, ok := original.Params[p.pos].(map[string]any); ok {
			if preparedObj, ok := prepared.Params[p.pos].(map[string]any); ok {
				if number, ok := originalObj["blockNumber"].(string); ok && rpc.IsMovingTag(number) {
					if hash, ok := preparedObj["blockHash"].(string); ok {
						return hash
					}
				}
			}
		}
	}
	return p.Decorator.BlockRef(original, prepared)
}

// ReferenceFor declares the reference this request needs. An EIP-1898
// object carrying a moving blockNumber names its level; a string at the
// position belongs to the inner anchored layer, so the declaration forwards.
// Explicit pins, pending, earliest, and quantities need none.
func (p eip1898Policy) ReferenceFor(req rpc.Request) (rpc.ReferenceRequest, bool) {
	if p.pos < 0 || p.pos >= len(req.Params) {
		return rpc.ReferenceRequest{}, false
	}
	if obj, ok := req.Params[p.pos].(map[string]any); ok {
		number, isStr := obj["blockNumber"].(string)
		if !isStr || !rpc.IsMovingTag(number) {
			return rpc.ReferenceRequest{}, false
		}
		return rpc.ReferenceRequest{Kind: rpc.ReferenceKindForTag(number)}, true
	}
	if inner, ok := p.Policy.(rpc.ReferenceRequirement); ok {
		return inner.ReferenceFor(req)
	}
	return rpc.ReferenceRequest{}, false
}

// MethodRule reports the EIP-1898 block-reference form. It does not rewrite
// a request. The corroborating wrapper promotes this method.
func (p eip1898Policy) MethodRule() (rpc.MethodRule, rpc.Policy) {
	return p.MethodRuleWith(rpc.MethodRule{BlockRef: rpc.RuleBlockRefEIP1898})
}

type corroboratingEIP1898Policy struct{ eip1898Policy }

func (p corroboratingEIP1898Policy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if inner, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		return inner.ResolveCorroborated(c, obs)
	}
	return p.Policy.Resolve(obs)
}

func (p corroboratingEIP1898Policy) ResolveOutcomeCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Outcome, error) {
	if inner, ok := p.Policy.(rpc.CorroboratingOutcomeResolver); ok {
		return inner.ResolveOutcomeCorroborated(c, obs)
	}
	decision, err := p.ResolveCorroborated(c, obs)
	return rpc.Outcome{Decision: decision}, err
}

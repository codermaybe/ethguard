package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AnchorFunc establishes a verified, immutable block reference. It is called
// only when a moving tag actually needs rewriting.
type AnchorFunc func(context.Context) (string, error)

var movingTags = map[string]bool{
	"latest":    true,
	"safe":      true,
	"finalized": true,
}

// WithAnchor decorates any policy with moving-tag rewriting at one or more
// parameter positions. The underlying policy still owns normalization and
// resolution; this decorator only prepares an immutable request for fan-out.
//
// The anchor is resolved at most once per request, even when several positions
// contain moving tags. Explicit numbers and hashes pass through untouched, as
// does pending: rewriting pending would silently change its meaning.
func WithAnchor(anchor AnchorFunc, positions ...int) func(Policy) Policy {
	return func(policy Policy) Policy {
		if policy == nil {
			policy = ForwardPolicy{}
		}
		return anchoredPolicy{policy: policy, anchor: anchor, positions: append([]int(nil), positions...)}
	}
}

type anchoredPolicy struct {
	policy    Policy
	anchor    AnchorFunc
	positions []int
}

func (a anchoredPolicy) Prepare(ctx context.Context, req Request) (Request, error) {
	var rewrite bool
	for _, pos := range a.positions {
		if pos < 0 || pos >= len(req.Params) {
			continue
		}
		tag, ok := req.Params[pos].(string)
		if ok && movingTags[strings.ToLower(tag)] {
			rewrite = true
			break
		}
	}
	if !rewrite {
		return a.policy.Prepare(ctx, req)
	}
	if a.anchor == nil {
		return Request{}, fmt.Errorf("anchor: missing anchor function")
	}
	hash, err := a.anchor(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("anchor: %w", err)
	}
	params := append([]any(nil), req.Params...)
	for _, pos := range a.positions {
		if pos < 0 || pos >= len(params) {
			continue
		}
		tag, ok := params[pos].(string)
		if ok && movingTags[strings.ToLower(tag)] {
			params[pos] = hash
		}
	}
	return a.policy.Prepare(ctx, Request{Method: req.Method, Params: params})
}

// BlockRef reports the anchor hash when this request actually rewrote at least
// one moving tag. Explicit numbers, hashes, and pending do not invent a pin.
func (a anchoredPolicy) BlockRef(original, prepared Request) string {
	for _, pos := range a.positions {
		if pos < 0 || pos >= len(original.Params) || pos >= len(prepared.Params) {
			continue
		}
		originalTag, ok := original.Params[pos].(string)
		if ok && movingTags[strings.ToLower(originalTag)] {
			if hash, ok := prepared.Params[pos].(string); ok {
				return hash
			}
		}
	}
	if provider, ok := a.policy.(BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (a anchoredPolicy) Normalize(value json.RawMessage) (string, error) {
	return a.policy.Normalize(value)
}

func (a anchoredPolicy) Resolve(obs []Observation) (Decision, error) {
	return a.policy.Resolve(obs)
}

// Anchored binds a normalizer to strict resolution and rewrites a moving block
// tag at position tagPos to a verified block hash before fan-out. Strict
// options (for example WithQuorum) pass through to the underlying resolution.
//
// It remains as a convenience constructor. WithAnchor is the general-purpose
// decorator for policies such as Median and multi-position log filters.
func Anchored(tagPos int, normalize func(json.RawMessage) (string, error), anchor AnchorFunc, opts ...StrictOption) Policy {
	return WithAnchor(anchor, tagPos)(Strict(normalize, opts...))
}

// RejectPending decorates a policy with an upfront rejection for the pending
// tag at selected parameter positions. This is intentionally separate from
// WithAnchor: pending must never be rewritten, and methods that cannot support
// pending should fail before contacting any upstream.
func RejectPending(positions ...int) func(Policy) Policy {
	return func(policy Policy) Policy {
		if policy == nil {
			policy = ForwardPolicy{}
		}
		return pendingRejectingPolicy{policy: policy, positions: append([]int(nil), positions...)}
	}
}

type pendingRejectingPolicy struct {
	policy    Policy
	positions []int
}

func (p pendingRejectingPolicy) Prepare(ctx context.Context, req Request) (Request, error) {
	for _, pos := range p.positions {
		if pos < 0 || pos >= len(req.Params) {
			continue
		}
		if tag, ok := req.Params[pos].(string); ok && strings.EqualFold(tag, "pending") {
			return Request{}, fmt.Errorf("rpc: pending is not supported for %s: %w", req.Method, ErrInvalidRequest)
		}
	}
	return p.policy.Prepare(ctx, req)
}

func (p pendingRejectingPolicy) BlockRef(original, prepared Request) string {
	if provider, ok := p.policy.(BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (p pendingRejectingPolicy) Normalize(value json.RawMessage) (string, error) {
	return p.policy.Normalize(value)
}

func (p pendingRejectingPolicy) Resolve(obs []Observation) (Decision, error) {
	return p.policy.Resolve(obs)
}

package rpc

// The legacy decorator family: the constructor-era anchoring decorators
// (WithAnchor, Anchored, RejectPending, WithAnchorHeight,
// WithAnchorHeightFields) and their corroborating wrappers. Registered read
// methods no longer execute through them — the plan model in read_plan.go
// drives every registered method — so this file is the deletion boundary for
// the legacy execution model. The shared block-reference helpers it consumes
// live in block_reference.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// WithAnchor decorates any policy with moving-tag rewriting at one or more
// parameter positions. The underlying policy still owns normalization and
// resolution; this decorator only prepares an immutable request for fan-out.
//
// The anchor is resolved at most once per request, even when several positions
// contain the same moving tag. The tag is carried through context to a
// tag-aware resolver; mixed moving tags are rejected rather than interchanged.
// Explicit numbers and hashes pass through untouched, as does pending.
func WithAnchor(anchor AnchorFunc, positions ...int) func(Policy) Policy {
	return func(policy Policy) Policy {
		if policy == nil {
			policy = ForwardPolicy{}
		}
		base := anchoredPolicy{Decorator: Decorator{Policy: policy}, anchor: anchor, positions: append([]int(nil), positions...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingAnchoredPolicy{base}
		}
		return base
	}
}

type anchoredPolicy struct {
	Decorator
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
		return a.Policy.Prepare(ctx, req)
	}
	if a.anchor == nil {
		return Request{}, fmt.Errorf("anchor: missing anchor function")
	}
	anchorCtx := ctx
	anchorTag := ""
	for _, pos := range a.positions {
		if pos >= 0 && pos < len(req.Params) {
			if tag, ok := req.Params[pos].(string); ok && movingTags[strings.ToLower(tag)] {
				tag = strings.ToLower(tag)
				if anchorTag != "" && anchorTag != tag {
					return Request{}, fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", req.Method, anchorTag, tag, ErrInvalidRequest)
				}
				anchorTag = tag
			}
		}
	}
	if anchorTag != "" {
		anchorCtx = WithAnchorTag(ctx, anchorTag)
	}
	hash, err := a.anchor(anchorCtx)
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
	return a.Policy.Prepare(ctx, Request{Method: req.Method, Params: params})
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
	return a.Decorator.BlockRef(original, prepared)
}

// MethodRule reports hash anchoring at this decorator's positions. It does
// not prepare or resolve the request. The corroborating wrapper promotes it.
func (a anchoredPolicy) MethodRule() (MethodRule, Policy) {
	return a.MethodRuleWith(MethodRule{Anchor: anchorRule("hash", a.positions)})
}

// ReferenceFor declares the reference this request needs: a moving tag at
// one of the anchored positions names its level. Mixed tags are a request
// error that Prepare raises, so the declaration reports "not needed" and
// lets the local rejection happen first. Explicit numbers, hashes, and
// pending need no reference.
func (a anchoredPolicy) ReferenceFor(req Request) (ReferenceRequest, bool) {
	tag, needed := singleMovingTag(req, a.positions)
	if !needed {
		return ReferenceRequest{}, false
	}
	return ReferenceRequest{Kind: ReferenceKindForTag(tag)}, true
}

func (a anchoredPolicy) Resolve(obs []Observation) (Decision, error) {
	return a.Policy.Resolve(obs)
}

// corroboratingAnchoredPolicy is the WithAnchor wrapper around an inner
// CorroboratingPolicy. A separate type keeps wrapping Median from looking
// like a corroborated read (T01 review P3) while wrapping Strict still
// forwards the invocation snapshot (T01 review P1).
type corroboratingAnchoredPolicy struct{ anchoredPolicy }

func (a corroboratingAnchoredPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(a.Policy, c, obs)
}

func (a corroboratingAnchoredPolicy) ResolveOutcomeCorroborated(c Corroboration, obs []Observation) (Outcome, error) {
	return resolveOutcomeCorroborated(a.Policy, c, obs)
}

// Anchored binds a normalizer to strict resolution and rewrites a moving block
// tag at position tagPos to a corroborated block hash before fan-out. Strict
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
//
// Choice (a): a position rejects the same pending spellings as
// methods.pendingTag — a case-insensitive string, a JSON string, or an
// EIP-1898 object whose blockNumber is pending (requireCanonical may be
// present). Other objects are not pending and stay in contract for the
// inner policy. WithAnchor still rewrites only string moving tags; this
// decorator is what stops a pending object from reaching a provider.
func RejectPending(positions ...int) func(Policy) Policy {
	return func(policy Policy) Policy {
		if policy == nil {
			policy = ForwardPolicy{}
		}
		base := pendingRejectingPolicy{Decorator: Decorator{Policy: policy}, positions: append([]int(nil), positions...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingPendingPolicy{base}
		}
		return base
	}
}

type pendingRejectingPolicy struct {
	Decorator
	positions []int
}

func (p pendingRejectingPolicy) Prepare(ctx context.Context, req Request) (Request, error) {
	for _, pos := range p.positions {
		if pos < 0 || pos >= len(req.Params) {
			continue
		}
		if pendingReference(req.Params[pos]) {
			return Request{}, fmt.Errorf("rpc: pending is not supported for %s: %w", req.Method, ErrInvalidRequest)
		}
	}
	return p.Policy.Prepare(ctx, req)
}

// MethodRule reports a local pending rejection. This decorator fails the
// request before any anchor or upstream call. It does not resolve a value.
func (p pendingRejectingPolicy) MethodRule() (MethodRule, Policy) {
	return p.MethodRuleWith(MethodRule{Pending: RulePendingLocalReject})
}

type corroboratingPendingPolicy struct{ pendingRejectingPolicy }

func (p corroboratingPendingPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(p.Policy, c, obs)
}

func (p corroboratingPendingPolicy) ResolveOutcomeCorroborated(c Corroboration, obs []Observation) (Outcome, error) {
	return resolveOutcomeCorroborated(p.Policy, c, obs)
}

// WithAnchorHeight decorates a policy with moving-tag rewriting to the
// corroborated reference's block height, spelled as a hex quantity. It exists
// for methods whose wire form cannot carry a hash (see AnchorHeightFunc);
// everywhere else WithAnchor's hash rewrite is the right shape.
//
// The height is resolved at most once per request, even when several
// positions contain moving tags. Explicit numbers, hashes, and pending pass
// through untouched.
func WithAnchorHeight(height AnchorHeightFunc, positions ...int) func(Policy) Policy {
	return func(policy Policy) Policy {
		if policy == nil {
			policy = ForwardPolicy{}
		}
		base := anchoredHeightPolicy{Decorator: Decorator{Policy: policy}, height: height, positions: append([]int(nil), positions...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingHeightPolicy{base}
		}
		return base
	}
}

type anchoredHeightPolicy struct {
	Decorator
	height    AnchorHeightFunc
	positions []int
}

func (a anchoredHeightPolicy) Prepare(ctx context.Context, req Request) (Request, error) {
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
		return a.Policy.Prepare(ctx, req)
	}
	if a.height == nil {
		return Request{}, fmt.Errorf("anchor: missing height function")
	}
	anchorCtx := ctx
	anchorTag := ""
	for _, pos := range a.positions {
		if pos >= 0 && pos < len(req.Params) {
			if tag, ok := req.Params[pos].(string); ok && movingTags[strings.ToLower(tag)] {
				tag = strings.ToLower(tag)
				if anchorTag != "" && anchorTag != tag {
					return Request{}, fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", req.Method, anchorTag, tag, ErrInvalidRequest)
				}
				anchorTag = tag
			}
		}
	}
	if anchorTag != "" {
		anchorCtx = WithAnchorTag(ctx, anchorTag)
	}
	height, err := a.height(anchorCtx)
	if err != nil {
		return Request{}, fmt.Errorf("anchor: %w", err)
	}
	ref := strconv.FormatUint(height, 16)
	params := append([]any(nil), req.Params...)
	for _, pos := range a.positions {
		if pos < 0 || pos >= len(params) {
			continue
		}
		tag, ok := params[pos].(string)
		if ok && movingTags[strings.ToLower(tag)] {
			params[pos] = "0x" + ref
		}
	}
	return a.Policy.Prepare(ctx, Request{Method: req.Method, Params: params})
}

// BlockRef reports the pinned height (hex spelling) when this request
// actually rewrote at least one moving tag.
func (a anchoredHeightPolicy) BlockRef(original, prepared Request) string {
	for _, pos := range a.positions {
		if pos < 0 || pos >= len(original.Params) || pos >= len(prepared.Params) {
			continue
		}
		originalTag, ok := original.Params[pos].(string)
		if ok && movingTags[strings.ToLower(originalTag)] {
			if ref, ok := prepared.Params[pos].(string); ok {
				return ref
			}
		}
	}
	return a.Decorator.BlockRef(original, prepared)
}

// MethodRule reports height anchoring at this decorator's positions. It does
// not prepare or resolve the request. The corroborating wrapper promotes it.
func (a anchoredHeightPolicy) MethodRule() (MethodRule, Policy) {
	return a.MethodRuleWith(MethodRule{Anchor: anchorRule("height", a.positions)})
}

// ReferenceFor declares the height reference this request needs; the wire
// form takes a height, so the established evidence is read at its height.
func (a anchoredHeightPolicy) ReferenceFor(req Request) (ReferenceRequest, bool) {
	tag, needed := singleMovingTag(req, a.positions)
	if !needed {
		return ReferenceRequest{}, false
	}
	return ReferenceRequest{Kind: ReferenceKindForTag(tag)}, true
}

type corroboratingHeightPolicy struct{ anchoredHeightPolicy }

func (a corroboratingHeightPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(a.Policy, c, obs)
}

func (a corroboratingHeightPolicy) ResolveOutcomeCorroborated(c Corroboration, obs []Observation) (Outcome, error) {
	return resolveOutcomeCorroborated(a.Policy, c, obs)
}

// WithAnchorHeightFields decorates a policy so that named string fields of
// the object-typed parameter at paramPos are rewritten to the corroborated
// reference's hex height — and defaulted to it when absent, since a
// provider's implicit default is a moving tag in field clothing.
// eth_getLogs needs this: its fromBlock/toBlock are BlockNumbers (a
// 32-byte hash there dies with "hex number > 64 bits", the soak's first
// catch), while blockHash is the dedicated, mutually exclusive hash
// position — a filter carrying blockHash pins its range explicitly and
// passes through untouched.
func WithAnchorHeightFields(height AnchorHeightFunc, paramPos int, fields ...string) func(Policy) Policy {
	return anchorFields(heightRef(height), paramPos, fields)
}

func anchorFields(resolve func(context.Context) (string, error), paramPos int, fields []string) func(Policy) Policy {
	return func(policy Policy) Policy {
		if policy == nil {
			policy = ForwardPolicy{}
		}
		base := anchoredFieldsPolicy{Decorator: Decorator{Policy: policy}, resolve: resolve, paramPos: paramPos, fields: append([]string(nil), fields...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingFieldsPolicy{base}
		}
		return base
	}
}

func heightRef(height AnchorHeightFunc) func(context.Context) (string, error) {
	if height == nil {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		h, err := height(ctx)
		if err != nil {
			return "", err
		}
		return "0x" + strconv.FormatUint(h, 16), nil
	}
}

type anchoredFieldsPolicy struct {
	Decorator
	resolve  func(context.Context) (string, error)
	paramPos int
	fields   []string
}

func (a anchoredFieldsPolicy) Prepare(ctx context.Context, req Request) (Request, error) {
	if a.paramPos < 0 || a.paramPos >= len(req.Params) {
		return a.Policy.Prepare(ctx, req)
	}
	obj, ok := req.Params[a.paramPos].(map[string]any)
	if !ok {
		return a.Policy.Prepare(ctx, req) // not an object (hash-pin form, malformed): upstream judges
	}
	if _, pinned := obj["blockHash"]; pinned {
		// blockHash addresses the range explicitly and excludes fromBlock/
		// toBlock on the wire. Coexisting bounds are contradictory input;
		// failing here keeps the read off the upstream entirely instead of
		// producing inevitable fleet dissent.
		for _, field := range a.fields {
			if _, present := obj[field]; present {
				return Request{}, fmt.Errorf("rpc: %s mixes blockHash with %q: %w",
					req.Method, field, ErrInvalidRequest)
			}
		}
		return a.Policy.Prepare(ctx, req) // explicit pin without range fields: complete, untouched
	}
	var rewrite bool
	anchorTag := ""
	for _, field := range a.fields {
		value, present := obj[field]
		if !present {
			rewrite = true // provider default for a missing bound is a moving tag
			break
		}
		if tag, isStr := value.(string); isStr && movingTags[strings.ToLower(tag)] {
			rewrite = true
			tag = strings.ToLower(tag)
			if anchorTag != "" && anchorTag != tag {
				return Request{}, fmt.Errorf("rpc: %s mixes moving tags %q and %q: %w", req.Method, anchorTag, tag, ErrInvalidRequest)
			}
			anchorTag = tag
		}
	}
	if !rewrite {
		return a.Policy.Prepare(ctx, req)
	}
	if a.resolve == nil {
		return Request{}, fmt.Errorf("anchor: missing anchor function")
	}
	anchorCtx := ctx
	if anchorTag != "" {
		anchorCtx = WithAnchorTag(ctx, anchorTag)
	}
	ref, err := a.resolve(anchorCtx)
	if err != nil {
		return Request{}, fmt.Errorf("anchor: %w", err)
	}
	filtered := make(map[string]any, len(obj))
	for key, value := range obj {
		filtered[key] = value
	}
	for _, field := range a.fields {
		value, present := filtered[field]
		if !present {
			filtered[field] = ref
			continue
		}
		if tag, isStr := value.(string); isStr && movingTags[strings.ToLower(tag)] {
			filtered[field] = ref
		}
	}
	params := append([]any(nil), req.Params...)
	params[a.paramPos] = filtered
	return a.Policy.Prepare(ctx, Request{Method: req.Method, Params: params})
}

// BlockRef reports the pinned reference spelling when this request actually
// rewrote or defaulted at least one filter field.
func (a anchoredFieldsPolicy) BlockRef(original, prepared Request) string {
	if a.paramPos >= 0 && a.paramPos < len(original.Params) && a.paramPos < len(prepared.Params) {
		originalObj, okOriginal := original.Params[a.paramPos].(map[string]any)
		preparedObj, okPrepared := prepared.Params[a.paramPos].(map[string]any)
		if okOriginal && okPrepared {
			if _, pinned := originalObj["blockHash"]; !pinned {
				for _, field := range a.fields {
					value, present := originalObj[field]
					if !present {
						if ref, ok := preparedObj[field].(string); ok {
							return ref
						}
					}
					if tag, isStr := value.(string); isStr && movingTags[strings.ToLower(tag)] {
						if ref, ok := preparedObj[field].(string); ok {
							return ref
						}
					}
				}
			}
		}
	}
	return a.Decorator.BlockRef(original, prepared)
}

// MethodRule reports height anchoring of named fields. It does not prepare
// or resolve the request. The corroborating wrapper promotes it.
func (a anchoredFieldsPolicy) MethodRule() (MethodRule, Policy) {
	return a.MethodRuleWith(MethodRule{Anchor: anchorFieldsRule(a.paramPos, a.fields)})
}

// ReferenceFor declares the reference the filter fields need: a missing
// bound defaults to a moving tag, and an explicit moving tag names its
// level. A blockHash pin needs no reference; contradictory input stays
// Prepare's local rejection.
func (a anchoredFieldsPolicy) ReferenceFor(req Request) (ReferenceRequest, bool) {
	if a.paramPos < 0 || a.paramPos >= len(req.Params) {
		return ReferenceRequest{}, false
	}
	obj, ok := req.Params[a.paramPos].(map[string]any)
	if !ok {
		return ReferenceRequest{}, false
	}
	if _, pinned := obj["blockHash"]; pinned {
		return ReferenceRequest{}, false
	}
	tag := ""
	for _, field := range a.fields {
		value, present := obj[field]
		if !present {
			// A provider's implicit default is a moving tag in field clothing.
			tag = "latest"
			continue
		}
		if value, isStr := value.(string); isStr && movingTags[strings.ToLower(value)] {
			lowered := strings.ToLower(value)
			if tag != "" && tag != "latest" && tag != lowered {
				return ReferenceRequest{}, false
			}
			tag = lowered
		}
	}
	if tag == "" {
		return ReferenceRequest{}, false
	}
	return ReferenceRequest{Kind: ReferenceKindForTag(tag)}, true
}

type corroboratingFieldsPolicy struct{ anchoredFieldsPolicy }

func (a corroboratingFieldsPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(a.Policy, c, obs)
}

func (a corroboratingFieldsPolicy) ResolveOutcomeCorroborated(c Corroboration, obs []Observation) (Outcome, error) {
	return resolveOutcomeCorroborated(a.Policy, c, obs)
}

// resolveCorroborated forwards an invocation snapshot to the inner
// CorroboratingPolicy without bypassing intermediate decorators.
func resolveCorroborated(policy Policy, c Corroboration, obs []Observation) (Decision, error) {
	if inner, ok := corroboratingPolicy(policy); ok {
		return inner.ResolveCorroborated(c, obs)
	}
	return policy.Resolve(obs)
}

// resolveOutcomeCorroborated mirrors resolveCorroborated for the per-source
// outcome statement: the inner resolver states verdicts when it can, and a
// wrapped policy that cannot state them still resolves, without inventing a
// corroboration capability it never had.
func resolveOutcomeCorroborated(policy Policy, c Corroboration, obs []Observation) (Outcome, error) {
	if inner, ok := policy.(CorroboratingOutcomeResolver); ok {
		return inner.ResolveOutcomeCorroborated(c, obs)
	}
	decision, err := resolveCorroborated(policy, c, obs)
	return Outcome{Decision: decision}, err
}

// corroboratingPolicy reports the capability of the immediate wrapped policy.
// Constructors preserve it using distinct wrapper types, so forwarding through
// each layer neither invents a capability nor bypasses an intermediate policy.
func corroboratingPolicy(policy Policy) (CorroboratingPolicy, bool) {
	p, ok := policy.(CorroboratingPolicy)
	return p, ok
}

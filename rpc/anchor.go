package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AnchorFunc establishes a corroborated, immutable block reference. It is called
// only when a moving tag actually needs rewriting.
type AnchorFunc func(context.Context) (string, error)

// AnchorHeightFunc establishes a corroborated block height for methods whose
// wire form accepts a height but not a hash — eth_feeHistory's newestBlock
// is a BlockNumber, not a BlockNumberOrHash, so a hash cannot ride the wire
// (the soak caught the same constraint for eth_getBlockByNumber's number
// position). Both built-in resolvers derive the height from the same
// establishment response; no extra fan-out is required.
type AnchorHeightFunc func(context.Context) (uint64, error)

// movingTags is the anchor-rewrite set: latest, safe, and finalized. It is
// not the spelling authority for every block reference in the repo. The
// other spellings stay local on purpose:
//
//   - methods.validateEIP1898Object / isMovingTag: same three tags are
//     anchored; pending and earliest pass through on EIP-1898 objects.
//   - methods.pendingTag: pending only, including case, JSON, and
//     {blockNumber: pending}. RejectPending uses pendingReference below,
//     which recognizes that same pending shape.
//   - ethadapter.toBlockNumArg / blockRefArg: Geth negative numbers
//     (-1 pending, -2 latest, -3 finalized, -4 safe, -5 earliest as 0x0).
//     Typed BlockByNumber(-1) is rejected in blockByNegativeNumber and
//     never reaches this set.
//
// Do not fold these into one cross-package table: pending is not a moving
// tag, and earliest is an explicit height for some entry points.
var movingTags = map[string]bool{
	"latest":    true,
	"safe":      true,
	"finalized": true,
}

type anchorTagContextKey struct{}

func WithAnchorTag(ctx context.Context, tag string) context.Context {
	return context.WithValue(ctx, anchorTagContextKey{}, tag)
}
func AnchorTag(ctx context.Context) string {
	tag, _ := ctx.Value(anchorTagContextKey{}).(string)
	return tag
}

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
		base := anchoredPolicy{policy: policy, anchor: anchor, positions: append([]int(nil), positions...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingAnchoredPolicy{base}
		}
		return base
	}
}

type anchoredPolicy struct {
	policy    Policy
	anchor    AnchorFunc
	positions []int
}

func (a anchoredPolicy) Admit(ctx context.Context, req Request, value json.RawMessage, call AdmissionCaller) (json.RawMessage, error) {
	if p, ok := a.policy.(AdmissionPolicy); ok {
		return p.Admit(ctx, req, value, call)
	}
	return nil, nil
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

func (a anchoredPolicy) RequiresAgreement() bool {
	inner, ok := a.policy.(AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

func (a anchoredPolicy) SingleSource() bool {
	inner, ok := a.policy.(SingleSourcePolicy)
	return ok && inner.SingleSource()
}

func (a anchoredPolicy) IncludeRPCError() bool {
	return includesRPCError(a.policy)
}

func (a anchoredPolicy) Normalize(value json.RawMessage) (string, error) {
	return a.policy.Normalize(value)
}

// MethodRule reports hash anchoring at this decorator's positions. It does
// not prepare or resolve the request. The corroborating wrapper promotes it.
func (a anchoredPolicy) MethodRule() (MethodRule, Policy) {
	rule := MethodRule{Anchor: anchorRule("hash", a.positions)}
	if inner, ok := a.policy.(MethodRuleSource); ok {
		innerRule, _ := inner.MethodRule()
		rule = rule.WithInner(innerRule)
	}
	return rule, a.policy
}

func anchorRule(kind string, positions []int) string {
	if len(positions) == 0 {
		return kind
	}
	parts := make([]string, len(positions))
	for i, pos := range positions {
		parts[i] = strconv.Itoa(pos)
	}
	return kind + "@" + strings.Join(parts, ",")
}

func (a anchoredPolicy) Resolve(obs []Observation) (Decision, error) {
	return a.policy.Resolve(obs)
}

// corroboratingAnchoredPolicy is the WithAnchor wrapper around an inner
// CorroboratingPolicy. A separate type keeps wrapping Median from looking
// like a corroborated read (T01 review P3) while wrapping Strict still
// forwards the invocation snapshot (T01 review P1).
type corroboratingAnchoredPolicy struct{ anchoredPolicy }

func (a corroboratingAnchoredPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(a.policy, c, obs)
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
		base := pendingRejectingPolicy{policy: policy, positions: append([]int(nil), positions...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingPendingPolicy{base}
		}
		return base
	}
}

type pendingRejectingPolicy struct {
	policy    Policy
	positions []int
}

func (p pendingRejectingPolicy) Admit(ctx context.Context, req Request, value json.RawMessage, call AdmissionCaller) (json.RawMessage, error) {
	if inner, ok := p.policy.(AdmissionPolicy); ok {
		return inner.Admit(ctx, req, value, call)
	}
	return nil, nil
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
	return p.policy.Prepare(ctx, req)
}

// pendingReference reports pending spellings at one block-reference position.
// It matches methods.pendingTag: case-insensitive strings, JSON strings, and
// EIP-1898 objects whose blockNumber is pending. A blockHash object is not
// pending.
func pendingReference(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "pending")
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return false
		}
		return pendingReference(decoded)
	case map[string]any:
		number, ok := typed["blockNumber"]
		return ok && pendingReference(number)
	default:
		return false
	}
}

func (p pendingRejectingPolicy) BlockRef(original, prepared Request) string {
	if provider, ok := p.policy.(BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (p pendingRejectingPolicy) RequiresAgreement() bool {
	inner, ok := p.policy.(AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

func (p pendingRejectingPolicy) SingleSource() bool {
	inner, ok := p.policy.(SingleSourcePolicy)
	return ok && inner.SingleSource()
}

func (p pendingRejectingPolicy) IncludeRPCError() bool {
	return includesRPCError(p.policy)
}

func (p pendingRejectingPolicy) Normalize(value json.RawMessage) (string, error) {
	return p.policy.Normalize(value)
}

func (p pendingRejectingPolicy) Resolve(obs []Observation) (Decision, error) {
	return p.policy.Resolve(obs)
}

type corroboratingPendingPolicy struct{ pendingRejectingPolicy }

func (p corroboratingPendingPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(p.policy, c, obs)
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
		base := anchoredHeightPolicy{policy: policy, height: height, positions: append([]int(nil), positions...)}
		if _, ok := corroboratingPolicy(policy); ok {
			return corroboratingHeightPolicy{base}
		}
		return base
	}
}

type anchoredHeightPolicy struct {
	policy    Policy
	height    AnchorHeightFunc
	positions []int
}

func (a anchoredHeightPolicy) Admit(ctx context.Context, req Request, value json.RawMessage, call AdmissionCaller) (json.RawMessage, error) {
	if p, ok := a.policy.(AdmissionPolicy); ok {
		return p.Admit(ctx, req, value, call)
	}
	return nil, nil
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
		return a.policy.Prepare(ctx, req)
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
	return a.policy.Prepare(ctx, Request{Method: req.Method, Params: params})
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
	if provider, ok := a.policy.(BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (a anchoredHeightPolicy) RequiresAgreement() bool {
	inner, ok := a.policy.(AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

func (a anchoredHeightPolicy) SingleSource() bool {
	inner, ok := a.policy.(SingleSourcePolicy)
	return ok && inner.SingleSource()
}

func (a anchoredHeightPolicy) IncludeRPCError() bool {
	return includesRPCError(a.policy)
}

func (a anchoredHeightPolicy) Normalize(value json.RawMessage) (string, error) {
	return a.policy.Normalize(value)
}

func (a anchoredHeightPolicy) Resolve(obs []Observation) (Decision, error) {
	return a.policy.Resolve(obs)
}

type corroboratingHeightPolicy struct{ anchoredHeightPolicy }

func (a corroboratingHeightPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(a.policy, c, obs)
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
		base := anchoredFieldsPolicy{policy: policy, resolve: resolve, paramPos: paramPos, fields: append([]string(nil), fields...)}
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
	policy   Policy
	resolve  func(context.Context) (string, error)
	paramPos int
	fields   []string
}

func (a anchoredFieldsPolicy) Admit(ctx context.Context, req Request, value json.RawMessage, call AdmissionCaller) (json.RawMessage, error) {
	if p, ok := a.policy.(AdmissionPolicy); ok {
		return p.Admit(ctx, req, value, call)
	}
	return nil, nil
}

func (a anchoredFieldsPolicy) Prepare(ctx context.Context, req Request) (Request, error) {
	if a.paramPos < 0 || a.paramPos >= len(req.Params) {
		return a.policy.Prepare(ctx, req)
	}
	obj, ok := req.Params[a.paramPos].(map[string]any)
	if !ok {
		return a.policy.Prepare(ctx, req) // not an object (hash-pin form, malformed): upstream judges
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
		return a.policy.Prepare(ctx, req) // explicit pin without range fields: complete, untouched
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
		return a.policy.Prepare(ctx, req)
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
	return a.policy.Prepare(ctx, Request{Method: req.Method, Params: params})
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
	if provider, ok := a.policy.(BlockRefProvider); ok {
		return provider.BlockRef(original, prepared)
	}
	return ""
}

func (a anchoredFieldsPolicy) RequiresAgreement() bool {
	inner, ok := a.policy.(AgreementPolicy)
	return ok && inner.RequiresAgreement()
}

func (a anchoredFieldsPolicy) SingleSource() bool {
	inner, ok := a.policy.(SingleSourcePolicy)
	return ok && inner.SingleSource()
}

func (a anchoredFieldsPolicy) IncludeRPCError() bool {
	return includesRPCError(a.policy)
}

func (a anchoredFieldsPolicy) Normalize(value json.RawMessage) (string, error) {
	return a.policy.Normalize(value)
}

func (a anchoredFieldsPolicy) Resolve(obs []Observation) (Decision, error) {
	return a.policy.Resolve(obs)
}

type corroboratingFieldsPolicy struct{ anchoredFieldsPolicy }

func (a corroboratingFieldsPolicy) ResolveCorroborated(c Corroboration, obs []Observation) (Decision, error) {
	return resolveCorroborated(a.policy, c, obs)
}

// includesRPCError preserves the inner answer domain through Prepare decorators.
func includesRPCError(policy Policy) bool {
	provider, ok := policy.(RPCErrorPolicy)
	return ok && provider.IncludeRPCError()
}

// resolveCorroborated forwards an invocation snapshot to the inner
// CorroboratingPolicy without bypassing intermediate decorators.
func resolveCorroborated(policy Policy, c Corroboration, obs []Observation) (Decision, error) {
	if inner, ok := corroboratingPolicy(policy); ok {
		return inner.ResolveCorroborated(c, obs)
	}
	return policy.Resolve(obs)
}

// corroboratingPolicy reports the capability of the immediate wrapped policy.
// Constructors preserve it using distinct wrapper types, so forwarding through
// each layer neither invents a capability nor bypasses an intermediate policy.
func corroboratingPolicy(policy Policy) (CorroboratingPolicy, bool) {
	p, ok := policy.(CorroboratingPolicy)
	return p, ok
}

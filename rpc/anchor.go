package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AnchorFunc establishes a verified, immutable block reference. It is called
// only when a moving tag actually needs rewriting.
type AnchorFunc func(context.Context) (string, error)

// AnchorHeightFunc establishes a verified block height for methods whose
// wire form accepts a height but not a hash — eth_feeHistory's newestBlock
// is a BlockNumber, not a BlockNumberOrHash, so a hash cannot ride the wire
// (the soak caught the same constraint for eth_getBlockByNumber's number
// position). Both built-in resolvers derive the height from the same
// establishment response; no extra fan-out is required.
type AnchorHeightFunc func(context.Context) (uint64, error)

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

// WithAnchorHeight decorates a policy with moving-tag rewriting to the
// verified reference's block height, spelled as a hex quantity. It exists
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
		return anchoredHeightPolicy{policy: policy, height: height, positions: append([]int(nil), positions...)}
	}
}

type anchoredHeightPolicy struct {
	policy    Policy
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
		return a.policy.Prepare(ctx, req)
	}
	if a.height == nil {
		return Request{}, fmt.Errorf("anchor: missing height function")
	}
	height, err := a.height(ctx)
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

// WithAnchorHeightFields decorates a policy so that named string fields of
// the object-typed parameter at paramPos are rewritten to the verified
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
		return anchoredFieldsPolicy{policy: policy, resolve: resolve, paramPos: paramPos, fields: append([]string(nil), fields...)}
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
	for _, field := range a.fields {
		value, present := obj[field]
		if !present {
			rewrite = true // provider default for a missing bound is a moving tag
			break
		}
		if tag, isStr := value.(string); isStr && movingTags[strings.ToLower(tag)] {
			rewrite = true
			break
		}
	}
	if !rewrite {
		return a.policy.Prepare(ctx, req)
	}
	if a.resolve == nil {
		return Request{}, fmt.Errorf("anchor: missing anchor function")
	}
	ref, err := a.resolve(ctx)
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

// includesRPCError preserves the inner answer domain through Prepare decorators.
func includesRPCError(policy Policy) bool {
	provider, ok := policy.(RPCErrorPolicy)
	return ok && provider.IncludeRPCError()
}

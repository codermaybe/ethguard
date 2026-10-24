package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

const anchorMethod = "anchor"

// ReferenceResolver establishes a corroborated block reference for moving-state reads.
type ReferenceResolver interface {
	AnchorBlockHash(context.Context) (string, error)
	AnchorBlockRef(context.Context) (string, uint64, error)
	AnchorReference(context.Context) (rpc.ReferenceEvidence, error)
	AnchorReferenceForTag(context.Context, string) (rpc.ReferenceEvidence, error)
}

// Backward-compatible interface aliases.
type AnchorResolver interface {
	AnchorBlockHash(context.Context) (string, error)
}

type AnchorHeightProvider interface {
	AnchorBlockRef(context.Context) (string, uint64, error)
}

type AnchorTagProvider interface {
	AnchorReferenceForTag(context.Context, string) (rpc.ReferenceEvidence, error)
}

type AnchorEvidenceProvider interface {
	AnchorReference(context.Context) (rpc.ReferenceEvidence, error)
}

// StrictHeadAnchor requires corroborated strict latest-head agreement.
type StrictHeadAnchor struct{ aggregate *aggregator.Aggregator }

// NewStrictHeadAnchor builds the default latest-head resolver.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return newStrictHeadAnchor(p, rec, 0, nil, opts...)
}

func newStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, timeout time.Duration, corrob *rpc.CorroborationShared, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return &StrictHeadAnchor{
		aggregate: aggregator.New(p, rpc.Registry{anchorMethod: anchorHeadPolicy{methods.BlockByNumberPolicy(opts...)}},
			aggregator.WithRecorder(rec),
			aggregator.WithCollectTimeout(timeout),
			aggregator.WithCorroboration(corrob)),
	}
}

func (a *StrictHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	d, err := a.aggregate.Aggregate(ctx, rpc.Request{Method: anchorMethod, Params: []any{"latest", false}})
	ref := rpc.ReferenceEvidence{
		Status:           d.Status,
		Origin:           "strict-head",
		ObservedAt:       time.Now(),
		Participants:     d.Participants,
		Support:          d.Support,
		Reject:           d.Reject,
		Faults:           d.Faults,
		SourceFaults:     d.SourceFaults,
		CollectionFaults: d.CollectionFaults,
	}
	if err != nil {
		return ref, err
	}
	ref.Hash, ref.Height, err = blockRefFromDecision(d)
	return ref, err
}

func (a *StrictHeadAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == "latest" {
		return a.AnchorReference(ctx)
	}
	d, err := a.aggregate.Aggregate(ctx, rpc.Request{Method: anchorMethod, Params: []any{tag, false}})
	ref := rpc.ReferenceEvidence{
		Tag:              tag,
		Status:           d.Status,
		Origin:           "tagged-head",
		ObservedAt:       time.Now(),
		Participants:     d.Participants,
		Support:          d.Support,
		Reject:           d.Reject,
		Faults:           d.Faults,
		SourceFaults:     d.SourceFaults,
		CollectionFaults: d.CollectionFaults,
	}
	if err != nil {
		return ref, err
	}
	ref.Hash, ref.Height, err = blockRefFromDecision(d)
	return ref, err
}

func (a *StrictHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, e
}

func (a *StrictHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, r.Height, e
}

// FinalizedHeadAnchor establishes a common reference from provider-reported
// finalized views.
type FinalizedHeadAnchor struct {
	pool    *pool.Pool
	rec     observe.Recorder
	maxLag  uint64
	quorum  int
	timeout time.Duration
}

type FinalizedAnchorOption func(*finalizedAnchorConfig)
type finalizedAnchorConfig struct{ maxLag uint64 }

const defaultMaxFinalizedLag = 32

// WithMaxFinalizedLag bounds lag behind the lower-median reported height.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return func(c *finalizedAnchorConfig) { c.maxLag = blocks }
}

// NewFinalizedHeadAnchor builds a common finalized-reference resolver.
func NewFinalizedHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...FinalizedAnchorOption) *FinalizedHeadAnchor {
	cfg := finalizedAnchorConfig{defaultMaxFinalizedLag}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &FinalizedHeadAnchor{pool: p, rec: rec, maxLag: cfg.maxLag}
}

func (a *FinalizedHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	quorum, mode := a.quorum, rpc.CorroborationMajority
	if value, ok := refcontext.Corroboration(ctx); ok {
		if snapshot, valid := value.(rpc.Corroboration); valid {
			quorum, mode = snapshot.Floor, snapshot.Mode
		}
	}
	return reference.Finalized(ctx, reference.PoolHeadReader{Pool: a.pool}, a.maxLag, quorum, mode, a.timeout, a.rec)
}

func (a *FinalizedHeadAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "latest" {
		mode, floor := rpc.CorroborationMajority, a.quorum
		if value, ok := refcontext.Corroboration(ctx); ok {
			if snapshot, valid := value.(rpc.Corroboration); valid {
				mode, floor = snapshot.Mode, snapshot.Floor
			}
		}
		if mode == rpc.CorroborationForwarded {
			floor = 0
		} else if floor < 1 {
			floor = 2
		}
		shared, err := rpc.NewCorroborationShared(mode, floor)
		if err != nil {
			return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, err
		}
		var opts []rpc.StrictOption
		if floor > 0 {
			opts = append(opts, rpc.WithQuorum(floor))
		}
		if mode == rpc.CorroborationUnanimity {
			opts = append(opts, rpc.WithUnanimity())
		}
		resolver := newStrictHeadAnchor(a.pool, a.rec, a.timeout, shared, opts...)
		return resolver.AnchorReferenceForTag(ctx, tag)
	}
	if tag == "finalized" || tag == "" {
		ref, err := a.AnchorReference(ctx)
		ref.Tag = tag
		return ref, err
	}
	if tag == "safe" {
		mode, floor := rpc.CorroborationMajority, a.quorum
		if value, ok := refcontext.Corroboration(ctx); ok {
			if snapshot, valid := value.(rpc.Corroboration); valid {
				mode, floor = snapshot.Mode, snapshot.Floor
			}
		}
		if mode == rpc.CorroborationForwarded {
			floor = 0
		} else if floor < 1 {
			floor = 2
		}
		shared, err := rpc.NewCorroborationShared(mode, floor)
		if err != nil {
			return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, err
		}
		var opts []rpc.StrictOption
		if floor > 0 {
			opts = append(opts, rpc.WithQuorum(floor))
		}
		if mode == rpc.CorroborationUnanimity {
			opts = append(opts, rpc.WithUnanimity())
		}
		resolver := newStrictHeadAnchor(a.pool, a.rec, a.timeout, shared, opts...)
		return resolver.AnchorReferenceForTag(ctx, tag)
	}
	return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown, Origin: "finalized-anchor"}, fmt.Errorf("anchor: tag %q is not supported by finalized resolver: %w", tag, rpc.ErrInvalidRequest)
}

func (a *FinalizedHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, e
}

func (a *FinalizedHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, r.Height, e
}

type anchorHeadPolicy struct{ rpc.Policy }

func (p anchorHeadPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	prepared, err := p.Policy.Prepare(ctx, req)
	if err != nil {
		return rpc.Request{}, err
	}
	prepared.Method = methods.MethodGetBlockByNumber
	return prepared, nil
}

func (p anchorHeadPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	d, err := p.Policy.Resolve(obs)
	d = restoreAnchorSourceValue(d, obs)
	return p.requireCorroborated(d, err)
}

func (p anchorHeadPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	var (
		d   rpc.Decision
		err error
	)
	if cp, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		d, err = cp.ResolveCorroborated(c, obs)
	} else {
		d, err = p.Policy.Resolve(obs)
	}
	d = restoreAnchorSourceValue(d, obs)
	return p.requireCorroborated(d, err)
}

// Anchor comparison uses the canonical head encoder, but the anchor decision
// is an internal reference observation and must preserve the winning source's
// wire spelling. The request path lowercases the derived hash separately.
func restoreAnchorSourceValue(d rpc.Decision, obs []rpc.Observation) rpc.Decision {
	if d.Status != rpc.Corroborated || d.Representative.ID == "" {
		return d
	}
	for _, item := range obs {
		if item.Source.ID == d.Representative.ID {
			d.Value = append(json.RawMessage(nil), item.Value...)
			break
		}
	}
	return d
}

func (p anchorHeadPolicy) requireCorroborated(d rpc.Decision, err error) (rpc.Decision, error) {
	if err == nil && d.Status != rpc.Corroborated {
		d.Status = rpc.Unknown
		err = fmt.Errorf("uncorroborated anchor: %w", rpc.ErrConflict)
	}
	return d, err
}

func blockRefFromDecision(d rpc.Decision) (string, uint64, error) {
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(d.Value, &head); err != nil || len(head.Hash) < 3 {
		return "", 0, fmt.Errorf("winning anchor carries no hash: %w", rpc.ErrMalformedResponse)
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	if err != nil {
		return "", 0, fmt.Errorf("invalid anchor height: %w", rpc.ErrMalformedResponse)
	}
	return strings.ToLower(head.Hash), height, nil
}

// resolveReference establishes the moving reference for this call.
// Freshness is invocation-local (decision 0026): this function stores
// nothing for a later call, and it applies no TTL. CachedAnchor still
// implements a TTL, but Client rejects a non-zero cache duration before
// that wrapper can be installed, so the call path does not reuse a
// reference. Caching was rejected because a stored head would be another
// call's evidence, not this call's establishment.
//
// ObservedAt is the establishment time. Tag-aware and strict resolvers
// set it, including on failure; the hash-only branch below sets it before
// the resolver call and clears Status to Unknown if that call fails.
//
// Failure stays inside Prepare. anchorHashOf and anchorHeightOf publish
// the evidence, then the anchored policy returns a wrapped anchor error
// and the aggregator does not fan out the business method. The published
// evidence keeps Status Unknown and the resolver's Faults.
func resolveReference(ctx context.Context, resolver AnchorResolver, tag string) (rpc.ReferenceEvidence, error) {
	if tag != "" && tag != "latest" {
		if provider, ok := resolver.(AnchorTagProvider); ok {
			return provider.AnchorReferenceForTag(ctx, tag)
		}
		return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, fmt.Errorf("anchor: resolver %T cannot establish tag %q: %w", resolver, tag, rpc.ErrInvalidRequest)
	}
	if p, ok := resolver.(AnchorEvidenceProvider); ok {
		return p.AnchorReference(ctx)
	}
	ref := rpc.ReferenceEvidence{Status: rpc.Selected, Origin: "custom", ObservedAt: time.Now()}
	var err error
	if p, ok := resolver.(AnchorHeightProvider); ok {
		ref.Hash, ref.Height, err = p.AnchorBlockRef(ctx)
	} else {
		ref.Hash, err = resolver.AnchorBlockHash(ctx)
	}
	if err != nil {
		ref.Status = rpc.Unknown
	}
	return ref, err
}

func anchorHashOf(resolver AnchorResolver) rpc.AnchorFunc {
	return func(ctx context.Context) (string, error) {
		tag := rpc.AnchorTag(ctx)
		ref, err := resolveReference(ctx, resolver, tag)
		ref.Tag = tag
		refcontext.Publish(ctx, ref)
		if err == nil && ref.Status != rpc.Corroborated {
			err = fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
		}
		return ref.Hash, err
	}
}

func anchorHeightOf(resolver AnchorResolver) rpc.AnchorHeightFunc {
	return func(ctx context.Context) (uint64, error) {
		if !hasAnchorHeight(resolver) {
			return 0, fmt.Errorf("anchor resolver %T does not implement AnchorHeightProvider: %w", resolver, rpc.ErrInvalidRequest)
		}
		tag := rpc.AnchorTag(ctx)
		ref, err := resolveReference(ctx, resolver, tag)
		ref.Tag = tag
		refcontext.Publish(ctx, ref)
		if err == nil && ref.Status != rpc.Corroborated {
			err = fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
		}
		return ref.Height, err
	}
}

// CachedAnchor retains one complete reference and its evidence for a bounded TTL.
//
// Client never installs it. New rejects a non-zero anchor cache duration
// (decision 0026) before the construction-site wrapper can run, so this
// type is unreachable from Client. It stays for direct construction and
// the existing cache tests; do not wire it back into Client, and do not
// delete it in this cycle. Moving references are established per call,
// with no reuse and no TTL.
type CachedAnchor struct {
	inner      AnchorResolver
	ttl        time.Duration
	mu         sync.Mutex
	value      rpc.ReferenceEvidence
	validUntil time.Time
	pending    chan struct{}
}

func NewCachedAnchor(inner AnchorResolver, ttl time.Duration) *CachedAnchor {
	return &CachedAnchor{inner: inner, ttl: ttl}
}

func (c *CachedAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := c.AnchorReference(ctx)
	return r.Hash, e
}

func (c *CachedAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	if !hasAnchorHeight(c.inner) {
		return "", 0, fmt.Errorf("anchor resolver %T does not implement AnchorHeightProvider: %w", c.inner, rpc.ErrInvalidRequest)
	}
	r, e := c.AnchorReference(ctx)
	return r.Hash, r.Height, e
}

func (c *CachedAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == "latest" {
		return c.AnchorReference(ctx)
	}
	if provider, ok := c.inner.(AnchorTagProvider); ok {
		return provider.AnchorReferenceForTag(ctx, tag)
	}
	return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, fmt.Errorf("anchor: resolver %T does not preserve tag %q: %w", c.inner, tag, rpc.ErrInvalidRequest)
}

func (c *CachedAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	if tag := rpc.AnchorTag(ctx); tag != "" && tag != "latest" {
		if provider, ok := c.inner.(AnchorTagProvider); ok {
			return provider.AnchorReferenceForTag(ctx, tag)
		}
		return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, fmt.Errorf("anchor: resolver %T does not preserve tag %q: %w", c.inner, tag, rpc.ErrInvalidRequest)
	}
	for {
		if err := ctx.Err(); err != nil {
			return rpc.ReferenceEvidence{}, err
		}
		c.mu.Lock()
		if c.value.Hash != "" && time.Now().Before(c.validUntil) {
			r := c.value.Clone()
			c.mu.Unlock()
			return r, nil
		}
		if wait := c.pending; wait != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return rpc.ReferenceEvidence{}, ctx.Err()
			case <-wait:
				continue
			}
		}
		done := make(chan struct{})
		c.pending = done
		c.mu.Unlock()
		ref, err := resolveReference(ctx, c.inner, "")
		if err == nil {
			err = ctx.Err()
		}
		c.mu.Lock()
		if err == nil {
			c.value = ref.Clone()
			c.validUntil = time.Now().Add(c.ttl)
		}
		c.pending = nil
		close(done)
		c.mu.Unlock()
		return ref, err
	}
}

// confirmedAnchor adapts custom resolvers.
type confirmedAnchor struct {
	inner   AnchorResolver
	pool    *pool.Pool
	rec     observe.Recorder
	quorum  int
	timeout time.Duration
}

func (a *confirmedAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	candidate, err := resolveReference(ctx, a.inner, "")
	if err != nil {
		return candidate, err
	}
	ref, err := reference.Custom(ctx, a.pool, candidate.Hash, a.quorum, a.timeout, a.rec)
	if err == nil {
		if hasAnchorHeight(a.inner) && ref.Height != candidate.Height {
			err = fmt.Errorf("custom reference height mismatch: %w", rpc.ErrMalformedResponse)
		}
	}
	return ref, err
}

func (a *confirmedAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == "latest" {
		return a.AnchorReference(ctx)
	}
	return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown, Origin: "custom-reference"}, fmt.Errorf("anchor: custom resolver cannot establish tag %q: %w", tag, rpc.ErrInvalidRequest)
}

func (a *confirmedAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, e
}

func (a *confirmedAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	if !hasAnchorHeight(a.inner) {
		return "", 0, fmt.Errorf("anchor resolver %T does not implement AnchorHeightProvider: %w", a.inner, rpc.ErrInvalidRequest)
	}
	r, e := a.AnchorReference(ctx)
	return r.Hash, r.Height, e
}

func hasAnchorHeight(resolver AnchorResolver) bool {
	switch r := resolver.(type) {
	case *CachedAnchor:
		return hasAnchorHeight(r.inner)
	case *confirmedAnchor:
		return hasAnchorHeight(r.inner)
	default:
		_, ok := resolver.(AnchorHeightProvider)
		return ok
	}
}

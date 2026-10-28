package ethguard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

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

// StrictHeadAnchor requires corroborated strict latest-head agreement. The
// establishment algorithm is runtime-owned; the type stays as the public
// facade of that service.
type StrictHeadAnchor struct{ service *runtime.StrictHeadService }

// NewStrictHeadAnchor builds the default latest-head resolver.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return newStrictHeadAnchor(p, rec, 0, nil, opts...)
}

func newStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, timeout time.Duration, corrob *rpc.CorroborationShared, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return &StrictHeadAnchor{
		service: runtime.NewStrictHeadService(runtime.StrictHeadConfig{
			Pool:           p,
			Recorder:       rec,
			CollectTimeout: timeout,
			Corroboration:  corrob,
			StrictOptions:  opts,
		}),
	}
}

func (a *StrictHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return a.service.Reference(ctx)
}

func (a *StrictHeadAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	return a.service.ReferenceForTag(ctx, tag)
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
	service *runtime.FinalizedHeadService
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
	return &FinalizedHeadAnchor{
		pool:    p,
		rec:     rec,
		service: runtime.NewFinalizedHeadService(p, rec, cfg.maxLag, 0, 0),
		maxLag:  cfg.maxLag,
	}
}

// tuneForRuntime applies the client's adjudication floor and collect timeout
// after construction. The public option set cannot carry them (the floor is
// derived from the source count at New time), so New re-tunes the service
// once, before any call.
func (a *FinalizedHeadAnchor) tuneForRuntime(quorum int, timeout time.Duration) {
	a.quorum, a.timeout = quorum, timeout
	a.service = runtime.NewFinalizedHeadService(a.pool, a.rec, a.maxLag, quorum, timeout)
}

func (a *FinalizedHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return a.service.Reference(ctx)
}

func (a *FinalizedHeadAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	return a.service.ReferenceForTag(ctx, tag)
}

func (a *FinalizedHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, e
}

func (a *FinalizedHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, r.Height, e
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

func hasAnchorHeight(resolver AnchorResolver) bool {
	switch r := resolver.(type) {
	case *CachedAnchor:
		return hasAnchorHeight(r.inner)
	default:
		_, ok := resolver.(AnchorHeightProvider)
		return ok
	}
}

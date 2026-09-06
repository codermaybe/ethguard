package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"ethguard/aggregator"
	"ethguard/internal/hexqty"
	"ethguard/internal/refcontext"
	"ethguard/internal/reference"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

const anchorMethod = "anchor"

// AnchorResolver supplies a candidate reference for moving-state reads.
// Existing implementations remain usable; the client confirms their candidate
// through its configured fleet before using it for business reads.
type AnchorResolver interface {
	AnchorBlockHash(context.Context) (string, error)
}

// AnchorHeightProvider supplies a hash and height from the same establishment.
type AnchorHeightProvider interface {
	AnchorBlockRef(context.Context) (string, uint64, error)
}

// AnchorEvidenceProvider is the optional complete-reference capability. Built-in
// resolvers and their cache retain provenance without changing ethclient methods.
type AnchorEvidenceProvider interface {
	AnchorReference(context.Context) (rpc.ReferenceEvidence, error)
}

// StrictHeadAnchor requires corroborated strict latest-head agreement.
type StrictHeadAnchor struct{ aggregate *aggregator.Aggregator }

// NewStrictHeadAnchor builds the default latest-head resolver.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return newStrictHeadAnchor(p, rec, 0, opts...)
}
func newStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, timeout time.Duration, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return &StrictHeadAnchor{aggregator.New(p, rpc.Registry{anchorMethod: anchorHeadPolicy{methods.BlockByNumberPolicy(opts...)}}, aggregator.WithRecorder(rec), aggregator.WithCollectTimeout(timeout))}
}
func (a *StrictHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	d, err := a.aggregate.Aggregate(ctx, rpc.Request{Method: anchorMethod, Params: []any{"latest", false}})
	ref := rpc.ReferenceEvidence{Status: d.Status, Origin: "strict-head", ObservedAt: time.Now(), Participants: d.Participants, Support: d.Support, Reject: d.Reject, Faults: d.Faults}
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
// finalized views. Different heights are reconciled at one candidate height.
// This is source-backed evidence, not a consensus proof of finality.
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
	return reference.Finalized(ctx, a.pool, a.maxLag, a.quorum, a.timeout, a.rec)
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
	if err == nil && d.Status != rpc.Verified {
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
	height, err := hexqty.Parse(head.Number)
	if err != nil {
		return "", 0, fmt.Errorf("invalid anchor height: %w", rpc.ErrMalformedResponse)
	}
	return head.Hash, height, nil
}

func anchorHashOf(resolver AnchorResolver) rpc.AnchorFunc {
	return func(ctx context.Context) (string, error) {
		ref, err := resolveReference(ctx, resolver)
		refcontext.Publish(ctx, ref)
		if err == nil && ref.Status != rpc.Verified {
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
		ref, err := resolveReference(ctx, resolver)
		refcontext.Publish(ctx, ref)
		if err == nil && ref.Status != rpc.Verified {
			err = fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
		}
		return ref.Height, err
	}
}
func resolveReference(ctx context.Context, resolver AnchorResolver) (rpc.ReferenceEvidence, error) {
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
	return ref, err
}

// CachedAnchor retains one complete reference and its evidence for a bounded
// TTL. Each waiter may cancel independently. Failed establishments are not
// cached, and cancellation never promotes a failed establishment to success.
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
func (c *CachedAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
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
		ref, err := resolveReference(ctx, c.inner)
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

// confirmedAnchor adapts existing custom resolvers without new caller methods.
// A custom choice is confirmed as a block identity, not as on-chain finality.
type confirmedAnchor struct {
	inner   AnchorResolver
	pool    *pool.Pool
	rec     observe.Recorder
	quorum  int
	timeout time.Duration
}

func (a *confirmedAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	candidate, err := resolveReference(ctx, a.inner)
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

package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"ethguard/aggregator"
	"ethguard/internal/hexqty"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// anchorMethod labels observability events for anchored reads establishing
// their block reference. A fixed literal keeps label cardinality bounded
// (decision 0008); it doubles as the internal registry key under which the
// anchor pipeline resolves, never reaching the wire.
const anchorMethod = "anchor"

// AnchorResolver establishes verified block references behind moving-tag
// reads (latest/safe/finalized). It is the replaceable anchoring strategy:
// implementations decide how a reference is chosen and how stale it may be,
// while call orchestration stays fixed. The default is StrictHeadAnchor;
// WithFinalizedAnchoring opts into conservative selection and
// NewCachedAnchor adds time-bounded reuse — decorators of this same seam.
type AnchorResolver interface {
	// AnchorBlockHash returns one block hash every source will be asked
	// about, or an error explaining why no trusted reference was reached.
	AnchorBlockHash(ctx context.Context) (string, error)
}

// StrictHeadAnchor is the default AnchorResolver: fan out a latest-block
// query through the pool and accept it only when sources agree under the
// block-head policy's strict consensus. If sources sit on different heads
// the resolution fails with conflict evidence rather than guessing which
// head is right.
//
// The pipeline rides the ordinary aggregation flow — collect, resolve,
// record — under the bounded "anchor" label, and its registry holds only
// the unanchored head policy, so establishment cannot recurse.
type StrictHeadAnchor struct {
	aggregate *aggregator.Aggregator
}

// NewStrictHeadAnchor builds the default resolver over p, reporting one
// decision event per establishment into rec.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, strictOpts ...rpc.StrictOption) *StrictHeadAnchor {
	if rec == nil {
		rec = observe.Nop
	}
	return &StrictHeadAnchor{
		aggregate: aggregator.New(p, rpc.Registry{
			anchorMethod: anchorHeadPolicy{methods.BlockByNumberPolicy(strictOpts...)},
		}, aggregator.WithRecorder(rec)),
	}
}

// AnchorBlockHash implements AnchorResolver.
func (a *StrictHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	hash, _, err := a.establish(ctx)
	return hash, err
}

// AnchorBlockRef implements AnchorHeightProvider: hash and height come from
// the same establishment response, so height-anchored methods cost nothing
// extra.
func (a *StrictHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	return a.establish(ctx)
}

func (a *StrictHeadAnchor) establish(ctx context.Context) (string, uint64, error) {
	decision, err := a.aggregate.Aggregate(ctx, rpc.Request{
		Method: anchorMethod,
		Params: []any{"latest", false},
	})
	if err != nil {
		return "", 0, err
	}
	return blockRefFromDecision(decision)
}

// FinalizedHeadAnchor is the conservative AnchorResolver: fan out a
// finalized-tag query and pin the reference at the lowest finalized height
// that still counts as fresh. The reference is what providers report, not
// a consensus-verified fact: safety rests on the fleet's trustworthiness
// and cross-source consistency, never on a proof of on-chain finality. The
// freshness bound and lowest-height selection keep a stale or lying
// minority from moving the pin; they do not make a colluding majority
// harmless.
//
// Selection rules over the fleet's finalized answers:
//
//   - The freshness reference is the lower median of the fleet's reported
//     heights, so a minority lying about height cannot vote honest views
//     out of the selection — the same collusion resistance as the tip
//     gate's reference.
//   - Sources trailing that reference by more than MaxFinalizedLag are
//     excluded as stale — filter evidence, not a health punishment.
//   - The remaining lowest height wins; sources reporting it must agree on
//     its hash, otherwise the call fails with conflict evidence (a fork).
//   - One uncorroborated report of the winning height still pins the anchor
//     (Selected class): bounded by the freshness bound, and safe only under
//     the source-trust assumption above.
//
// The pipeline rides the same aggregation flow and "anchor" label as
// StrictHeadAnchor and cannot recurse for the same reason.
type FinalizedHeadAnchor struct {
	aggregate *aggregator.Aggregator
}

// FinalizedAnchorOption tunes a FinalizedHeadAnchor at construction time.
type FinalizedAnchorOption func(*finalizedAnchorConfig)

type finalizedAnchorConfig struct {
	maxLag uint64
}

// defaultMaxFinalizedLag bounds how far a source's finalized view may trail
// the freshest observed one before being excluded: about six minutes of
// slots at mainnet cadence — generous against clock and propagation skew,
// tight enough to freeze out genuinely stalled providers.
const defaultMaxFinalizedLag = 32

// WithMaxFinalizedLag overrides how far behind the freshest observed
// finalized height a source may trail before its view is excluded as stale.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return func(c *finalizedAnchorConfig) { c.maxLag = blocks }
}

// NewFinalizedHeadAnchor builds the conservative resolver over p, reporting
// one decision event per establishment into rec.
func NewFinalizedHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...FinalizedAnchorOption) *FinalizedHeadAnchor {
	cfg := finalizedAnchorConfig{maxLag: defaultMaxFinalizedLag}
	for _, opt := range opts {
		opt(&cfg)
	}
	if rec == nil {
		rec = observe.Nop
	}
	return &FinalizedHeadAnchor{
		aggregate: aggregator.New(p, rpc.Registry{
			anchorMethod: anchorHeadPolicy{rpc.FinalizedHead(cfg.maxLag)},
		}, aggregator.WithRecorder(rec)),
	}
}

// AnchorBlockHash implements AnchorResolver.
func (a *FinalizedHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	hash, _, err := a.establish(ctx)
	return hash, err
}

// AnchorBlockRef implements AnchorHeightProvider: hash and height come from
// the same establishment response, so height-anchored methods cost nothing
// extra.
func (a *FinalizedHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	return a.establish(ctx)
}

func (a *FinalizedHeadAnchor) establish(ctx context.Context) (string, uint64, error) {
	decision, err := a.aggregate.Aggregate(ctx, rpc.Request{
		Method: anchorMethod,
		Params: []any{"finalized", false},
	})
	if err != nil {
		return "", 0, err
	}
	return blockRefFromDecision(decision)
}

// anchorHeadPolicy adapts the unanchored block-head policy to the anchor
// pipeline: events report under "anchor", while the fleet is asked the real
// wire method.
type anchorHeadPolicy struct {
	rpc.Policy
}

func (p anchorHeadPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	prepared, err := p.Policy.Prepare(ctx, req)
	if err != nil {
		return rpc.Request{}, err
	}
	prepared.Method = methods.MethodGetBlockByNumber
	return prepared, nil
}

// blockRefFromDecision extracts the anchor hash and height from a resolved
// head. The height rides along for free: every anchor policy normalizes the
// block's number, so height-anchored methods (feeHistory) need no extra
// fan-out to learn it.
func blockRefFromDecision(decision rpc.Decision) (string, uint64, error) {
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(decision.Value, &head); err != nil || len(head.Hash) < 3 {
		return "", 0, fmt.Errorf("%w: winning anchor block carries no hash", rpc.ErrMalformedResponse)
	}
	height, err := hexqty.Parse(head.Number)
	if err != nil {
		return "", 0, fmt.Errorf("%w: winning anchor block carries an invalid height %q",
			rpc.ErrMalformedResponse, head.Number)
	}
	return head.Hash, height, nil
}

// AnchorHeightProvider is an optional AnchorResolver capability for methods
// whose wire form accepts a height but not a hash (eth_feeHistory's
// newestBlock is a BlockNumber, not a BlockNumberOrHash). The capability
// returns the reference as a pair so one establishment serves both shapes;
// both built-in resolvers provide it, and a custom resolver without it
// simply leaves those methods unavailable, failing their reads with a clear
// error at Prepare.
type AnchorHeightProvider interface {
	AnchorBlockRef(ctx context.Context) (hash string, height uint64, err error)
}

// anchorHeightOf exposes a resolver's height capability to height-anchored
// policies at the composition root.
func anchorHeightOf(resolver AnchorResolver) rpc.AnchorHeightFunc {
	if hp, ok := resolver.(AnchorHeightProvider); ok {
		return func(ctx context.Context) (uint64, error) {
			_, height, err := hp.AnchorBlockRef(ctx)
			return height, err
		}
	}
	return func(context.Context) (uint64, error) {
		return 0, fmt.Errorf("ethguard: anchor resolver %T does not implement AnchorHeightProvider", resolver)
	}
}

// CachedAnchor decorates any AnchorResolver with time-bounded reuse: the
// first establishment inside a TTL window is shared by every moving-tag
// read, so anchored calls cost one fan-out instead of two. Failures are
// never cached; concurrent misses serialize on one establishment.
//
// Safety note: over a FinalizedHeadAnchor the pinned block is irreversible,
// so caching cannot regress safety — it only bounds liveness of progress.
// Over StrictHeadAnchor the head moves, and the TTL bounds how far behind
// the chain reads may land; size it against the caller's freshness needs
// (see decision 0007, anchor caching amendment).
type CachedAnchor struct {
	inner AnchorResolver
	ttl   time.Duration

	mu         sync.Mutex
	hash       string
	height     uint64
	validUntil time.Time
}

// NewCachedAnchor wraps inner so its references are reused for ttl.
func NewCachedAnchor(inner AnchorResolver, ttl time.Duration) *CachedAnchor {
	return &CachedAnchor{inner: inner, ttl: ttl}
}

// AnchorBlockHash implements AnchorResolver.
func (c *CachedAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	hash, _, err := c.establish(ctx)
	return hash, err
}

// AnchorBlockRef implements AnchorHeightProvider, sharing the cached
// establishment with hash reads. An inner resolver without the height
// capability fails height reads with a clear error instead of silently
// pinning reads to a bogus height.
func (c *CachedAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	if _, ok := c.inner.(AnchorHeightProvider); !ok {
		return "", 0, fmt.Errorf("ethguard: anchor resolver %T does not implement AnchorHeightProvider", c.inner)
	}
	return c.establish(ctx)
}

func (c *CachedAnchor) establish(ctx context.Context) (string, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hash != "" && time.Now().Before(c.validUntil) {
		return c.hash, c.height, nil
	}
	hp, ok := c.inner.(AnchorHeightProvider)
	if !ok {
		// Height-incapable inners serve hash reads only; the zero height in
		// the cache entry is never returned as an answer (AnchorBlockRef
		// rejects first).
		hash, err := c.inner.AnchorBlockHash(ctx)
		if err != nil {
			return "", 0, err
		}
		c.hash = hash
		c.height = 0
		c.validUntil = time.Now().Add(c.ttl)
		return hash, 0, nil
	}
	hash, height, err := hp.AnchorBlockRef(ctx)
	if err != nil {
		return "", 0, err
	}
	c.hash = hash
	c.height = height
	c.validUntil = time.Now().Add(c.ttl)
	return hash, height, nil
}

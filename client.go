package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// Client is the advanced entry point for generic calls and decision evidence.
// Prefer ethadapter.Client for ethclient-shaped application reads.
// Multiple goroutines may use one Client concurrently; connections are owned
// and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	policies   rpc.Registry
	// plans are the read methods driven by explicit execution plans; their
	// resolvers own canonical normalization at the facade boundary.
	plans    map[string]rpc.ReadPlan
	resolver AnchorResolver
	// corrob is the internal adjudication snapshot source; callers do not
	// configure its mode or support floor.
	corrob *rpc.CorroborationShared
	// collectTimeout bounds each write fan-out; the read aggregator owns its
	// own copy supplied at construction.
	collectTimeout time.Duration
}

// HTTPAdapter names the connection adapter a Client's pool dials upstreams
// with. HTTPAdapterNative selects the project-owned HTTP adapter and is the
// strategic default; HTTPAdapterGeth names the go-ethereum compatibility
// adapter, which the default build does not link — a bare selection fails
// construction with a pointer to ethguard/gethcompat, the explicit
// construction entry for that channel (gethcompat composes this option with
// its own dialer). The selection entry is retained for one migration cycle
// as this diagnostic. The naming is owned by the runtime composition root.
type HTTPAdapter = runtime.Adapter

const (
	HTTPAdapterGeth   = runtime.AdapterGeth
	HTTPAdapterNative = runtime.AdapterNative
)

// WithHTTPAdapter selects the connection adapter for this Client. An empty
// name keeps the strategic native default; only the native and geth names are
// known, and a bare geth selection fails construction with the seam pointer
// above.
func WithHTTPAdapter(a HTTPAdapter) Option {
	return func(o *clientOptions) { o.httpAdapter = a }
}

// Option tunes a Client at construction time.
type Option func(*clientOptions)

// DialerOption installs an explicit connection adapter dialer for the pool.
// It exists for the gethcompat construction seam, which links the go-ethereum
// compatibility adapter; production code must not use it to grow a second
// channel-selection surface. The import interlock pins gethcompat as the only
// production importer of the compatibility adapter.
func DialerOption(d transport.Dialer) Option {
	return func(o *clientOptions) {
		if d != nil {
			o.dialer = d
		}
	}
}

type clientOptions struct {
	logger         *slog.Logger
	recorder       observe.Recorder
	anchorCache    time.Duration
	tipEnabled     bool
	tipInterval    time.Duration
	tipMaxBehind   uint64
	collectTimeout time.Duration
	httpAdapter    HTTPAdapter
	// anchoring carries the business-read establishment strategy: the
	// finalized preset selects the candidate-selection algorithm with its
	// lag bound. The facade resolver keeps its own configuration.
	useFinalizedAnchoring bool
	finalizedMaxLag       uint64
	// dialer is a pool dialer injected by DialerOption (the gethcompat
	// seam); the default adapter selection yields to it.
	dialer transport.Dialer
}

// WithLogger injects a structured logger receiving one event per aggregate
// decision (status, conflict, fault count, per-source RTT). It is ignored
// when WithRecorder supplies an explicit sink.
func WithLogger(l *slog.Logger) Option {
	return func(o *clientOptions) { o.logger = l }
}

// WithRecorder injects a custom observability sink, such as the collector
// from ethguard/observe/prometheus. It wins over WithLogger.
func WithRecorder(r observe.Recorder) Option {
	return func(o *clientOptions) { o.recorder = r }
}

// WithCollectTimeout bounds each aggregate collection or forwarded attempt.
func WithCollectTimeout(timeout time.Duration) Option {
	if timeout <= 0 {
		panic("ethguard: WithCollectTimeout: timeout must be positive")
	}
	return func(o *clientOptions) { o.collectTimeout = timeout }
}

// WithFinalizedAnchoring opts into conservative anchor selection: the block
// reference is pinned at the lowest finalized height that is still fresh
// across sources (the runtime's finalized head resolver), so divergent latest
// tips no longer fail anchored reads. Options tune the freshness bound and
// similar knobs.
func WithFinalizedAnchoring(opts ...FinalizedAnchorOption) Option {
	return func(o *clientOptions) {
		o.useFinalizedAnchoring = true
		o.finalizedMaxLag = runtime.FinalizedAnchorLag(opts...)
	}
}

// WithTipTracking enables pool-side block-tip sampling and the freshness
// eligibility gate: sources trailing the fleet's robust reference height by
// more than maxBehind blocks are skipped by fan-outs until they catch up or
// their sample recovers (pool.WithTipTracking). Zero values select the
// documented defaults (10s sampling, 32-block bound), applied by pool from
// config's single source of defaults (decision 0004).
func WithTipTracking(every time.Duration, maxBehind uint64) Option {
	return func(o *clientOptions) {
		o.tipEnabled = true
		o.tipInterval = every
		o.tipMaxBehind = maxBehind
	}
}

// New builds a Client from an already-loaded configuration.
// Upstreams are registered into the RPC pool with endpoint deduplication.
// Dialing each source is deferred until its first call.
//
// Precedence between the two construction paths: an explicit Go option
// always wins over a value derived from the configuration, and a zero
// option value falls through to the documented default. Duration strings
// in the configuration were validated by cfg.Validate; the parses below
// cannot fail for a Config that passed validation.
func New(cfg config.Config, opts ...Option) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	co := clientOptions{collectTimeout: config.DefaultCollectTimeout()}
	if cfg.CollectTimeout != "" {
		co.collectTimeout, _ = time.ParseDuration(cfg.CollectTimeout)
	}
	if cfg.AnchorCacheTTL != "" {
		co.anchorCache, _ = time.ParseDuration(cfg.AnchorCacheTTL)
	}
	if cfg.TipTracking != nil {
		co.tipEnabled = true
		// Empty strings and a zero bound fall through to pool's defaults,
		// the same ones the Go option path relies on.
		if cfg.TipTracking.Interval != "" {
			co.tipInterval, _ = time.ParseDuration(cfg.TipTracking.Interval)
		}
		co.tipMaxBehind = cfg.TipTracking.MaxBehind
	}
	// The preset comparison mirrors config.Validate's normalization exactly:
	// a case-spelling that passes validation must select the same strategy
	// here, or the YAML path silently changes anchoring behavior (decision
	// 0004's failure mode).
	if cfg.Anchoring != nil && strings.EqualFold(strings.TrimSpace(cfg.Anchoring.Preset), "finalized") {
		maxLag := cfg.Anchoring.MaxLag
		if maxLag == 0 {
			maxLag = config.DefaultFinalizedLag()
		}
		co.useFinalizedAnchoring, co.finalizedMaxLag = true, maxLag
	}
	for _, opt := range opts {
		opt(&co)
	}
	if co.anchorCache != 0 {
		return nil, fmt.Errorf("ethguard: anchor_cache_ttl is no longer supported; non-zero duration %s conflicts with decision 0026", co.anchorCache)
	}
	rec := co.recorder
	if rec == nil && co.logger != nil {
		rec = observe.NewLog(co.logger)
	}
	if rec == nil {
		rec = observe.Nop
	}
	var anchoring *runtime.AnchoringStrategy
	if co.useFinalizedAnchoring {
		anchoring = &runtime.AnchoringStrategy{Finalized: true, MaxLag: co.finalizedMaxLag}
	}
	var tip *runtime.TipTracking
	if co.tipEnabled {
		tip = &runtime.TipTracking{Interval: co.tipInterval, MaxBehind: co.tipMaxBehind}
	}
	runtimeBundle, err := runtime.Compose(runtime.Composition{
		ChainID:        cfg.ChainID,
		Upstreams:      cfg.Upstreams,
		Adapter:        co.httpAdapter,
		ExplicitDialer: co.dialer,
		TipTracking:    tip,
		Recorder:       rec,
		CollectTimeout: co.collectTimeout,
		Anchoring:      anchoring,
	})
	if err != nil {
		return nil, fmt.Errorf("ethguard: %w", err)
	}
	return &Client{
		pool:           runtimeBundle.Pool,
		aggregator:     runtimeBundle.Aggregator,
		policies:       runtimeBundle.Policies,
		plans:          runtimeBundle.Plans,
		resolver:       runtimeBundle.Resolver,
		corrob:         runtimeBundle.Corroboration,
		collectTimeout: co.collectTimeout,
	}, nil
}

// VotingMembers reports the currently configured membership. It is a
// diagnostic snapshot; an invocation already in flight keeps its own basis.
func (c *Client) VotingMembers() int { return c.pool.Len() }

// AnchorBlockHash returns a newly established corroborated block reference behind
// moving-tag reads through the library-owned anchor strategy.
func (c *Client) AnchorBlockHash(ctx context.Context) (string, error) {
	return c.resolver.AnchorBlockHash(ctx)
}

// AnchorBlockHashForTag is the tag-aware adapter seam behind moving-tag
// reads: it establishes the corroborated block reference for exactly the
// requested tag ("latest", "safe", or "finalized") and returns its hash.
// Each level keeps its meaning — safe is anchored at the safe reference,
// finalized at the finalized reference, never silently latest — and the
// returned evidence records the original tag. A reference that cannot reach
// Corroborated fails instead of returning an uncorroborated hash; pending
// and earliest are not moving tags and are rejected here (pending keeps its
// node-state path, earliest is an explicit height).
func (c *Client) AnchorBlockHashForTag(ctx context.Context, tag string) (string, error) {
	lowered := strings.ToLower(tag)
	switch lowered {
	case "latest", "safe", "finalized":
	default:
		return "", fmt.Errorf("ethguard: anchor tag %q is not an anchorable moving tag: %w", tag, rpc.ErrInvalidRequest)
	}
	ref, err := resolveReference(rpc.WithAnchorTag(ctx, lowered), c.resolver, lowered)
	ref.Tag = lowered
	refcontext.Publish(ctx, ref)
	if err == nil && ref.Status != rpc.Corroborated {
		err = fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
	}
	if err != nil {
		return "", err
	}
	return ref.Hash, nil
}

// AnchorBlockHeight returns the height of the currently corroborated block
// reference. It rides the same establishment as AnchorBlockHash — the
// response carries number and hash together, so no extra fan-out is
// needed — and errors clearly when a custom resolver cannot produce
// heights (AnchorHeightProvider).
func (c *Client) AnchorBlockHeight(ctx context.Context) (uint64, error) {
	hp, ok := c.resolver.(AnchorHeightProvider)
	if !ok {
		return 0, fmt.Errorf("ethguard: anchor resolver %T does not implement AnchorHeightProvider", c.resolver)
	}
	_, height, err := hp.AnchorBlockRef(ctx)
	return height, err
}

// Normalize reduces one raw response through the owning method's canonical
// form — the single owner of value parsing for a corroborated method.
// Typed callers use it instead of re-parsing raw JSON. A planned method
// normalizes through its plan's resolver; every other method through its
// compiled policy.
func (c *Client) Normalize(method string, raw json.RawMessage) (string, error) {
	if plan, ok := c.plans[method]; ok {
		return plan.Resolver.Normalize(raw)
	}
	return c.policies.PolicyFor(method).Normalize(raw)
}

// Pool exposes the underlying multi-source manager for advanced diagnostics.
// Membership is fixed at construction; the pool exposes no Join/Remove, and
// health/scheduling can never change the voting basis. Prefer Client methods
// for normal application code.
func (c *Client) Pool() *pool.Pool {
	return c.pool
}

// Close releases all upstream connections owned by the client.
// It is safe to call more than once.
func (c *Client) Close() {
	c.pool.Close()
}

// Mode reports the internally selected adapter route. It is derived from the
// configured source count and cannot be changed by callers.
func (c *Client) Mode() rpc.CorroborationMode { return c.corrob.Snapshot().Mode }

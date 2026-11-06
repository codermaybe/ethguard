package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// Client is EthGuard's only client: corroborated typed reads with
// ethclient-shaped signatures, the generic Call decision entry, and the
// write broadcast. Multiple goroutines may use one Client concurrently;
// connections are owned and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	// plans are the read methods driven by explicit execution plans; their
	// resolvers own canonical normalization at the facade boundary.
	plans map[string]rpc.ReadPlan
	// resolver is the runtime-owned anchor establishment strategy; the facade
	// only queries it (AnchorBlockHash and friends), never replaces it.
	resolver runtime.ReferenceResolver
	// corrob is the internal adjudication snapshot source; callers do not
	// configure its mode or support floor.
	corrob *rpc.CorroborationShared
	// collectTimeout bounds each write fan-out; the read aggregator owns its
	// own copy supplied at construction.
	collectTimeout time.Duration
	// lifeCtx is the owner lifecycle signal: Close cancels it with the
	// ErrPoolClosed cause, every public operation's merged context watches
	// it, and in-flight network work converges without depending on what
	// the caller passed as its own context.
	lifeCtx context.Context
	// cancelLife fires the lifecycle signal exactly once.
	cancelLife context.CancelCauseFunc
	// closeOnce makes the close sequence — the closed mark, the lifecycle
	// cancellation, and the shared pool close — a single atomic step;
	// concurrent and repeated calls all wait on the pool's one completion
	// signal.
	closeOnce sync.Once
	// closed is the closed mark new operations gate on.
	closed atomic.Bool
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
	// chainID pins the client to one Ethereum network when WithChainID was
	// used. A Go option always wins over the configuration value; nil leaves
	// cfg.ChainID untouched.
	chainID *uint64
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

// WithChainID pins the client to one Ethereum network: sources reporting a
// foreign chain are rejected before business fan-out (ErrChainIDMismatch).
// Zero leaves chain identity validation to the caller, matching the
// configuration field's meaning. The Go option wins over a chain_id in the
// configuration (New's documented precedence).
func WithChainID(chainID uint64) Option {
	return func(o *clientOptions) { o.chainID = &chainID }
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
	co := clientOptions{}
	for _, opt := range opts {
		opt(&co)
	}
	// The Go option always wins over a value derived from the configuration;
	// a zero option value falls through to the documented default, never to
	// the configuration (decision 0004's precedence rule).
	if co.collectTimeout == 0 && cfg.CollectTimeout != "" {
		co.collectTimeout, _ = time.ParseDuration(cfg.CollectTimeout)
	}
	if co.collectTimeout == 0 {
		co.collectTimeout = config.DefaultCollectTimeout()
	}
	if !co.tipEnabled && cfg.TipTracking != nil {
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
	if cfg.Anchoring != nil && !co.useFinalizedAnchoring && strings.EqualFold(strings.TrimSpace(cfg.Anchoring.Preset), "finalized") {
		maxLag := cfg.Anchoring.MaxLag
		if maxLag == 0 {
			maxLag = config.DefaultFinalizedLag()
		}
		co.useFinalizedAnchoring, co.finalizedMaxLag = true, maxLag
	}
	if co.chainID != nil {
		cfg.ChainID = *co.chainID
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.AnchorCacheTTL != "" {
		co.anchorCache, _ = time.ParseDuration(cfg.AnchorCacheTTL)
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
	lifeCtx, cancelLife := context.WithCancelCause(context.Background())
	return &Client{
		pool:           runtimeBundle.Pool,
		aggregator:     runtimeBundle.Aggregator,
		plans:          runtimeBundle.Plans,
		resolver:       runtimeBundle.Resolver,
		corrob:         runtimeBundle.Corroboration,
		collectTimeout: co.collectTimeout,
		lifeCtx:        lifeCtx,
		cancelLife:     cancelLife,
	}, nil
}

// VotingMembers reports the currently configured membership. It is a
// diagnostic snapshot; an invocation already in flight keeps its own basis.
func (c *Client) VotingMembers() int { return c.pool.Len() }

// Normalize reduces one raw response through the owning method's canonical
// form — the single owner of value parsing for a corroborated method.
// Typed callers use it instead of re-parsing raw JSON. Every registered
// method normalizes through its plan's resolver; an unregistered method has
// no canonical form and fails the call.
func (c *Client) Normalize(method string, raw json.RawMessage) (string, error) {
	plan, ok := c.plans[method]
	if !ok {
		return "", fmt.Errorf("ethguard: %s has no canonical normalizer: %w", method, rpc.ErrInvalidRequest)
	}
	return plan.Resolver.Normalize(raw)
}

// Pool exposes the underlying multi-source manager for advanced diagnostics.
// Membership is fixed at construction; the pool exposes no Join/Remove, and
// health/scheduling can never change the voting basis. Prefer Client methods
// for normal application code.
func (c *Client) Pool() *pool.Pool {
	return c.pool
}

// Close stops the client: it marks the client closed, cancels the owner
// lifecycle (in-flight operations converge — their merged contexts carry a
// recognizable ErrPoolClosed cause), and closes the shared pool. New
// operations fail with ErrPoolClosed before any network contact. Results
// already delivered to a caller are never rewritten.
//
// It is safe to call from any number of goroutines: the mark and the
// cancellation happen exactly once, and every caller — first or concurrent —
// waits on the pool's single completion signal, so Close returns only when
// the pool's background commitments (the tip sampler) have ended. It does
// not wait for callers to finish processing their received results.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancelLife(pool.ErrClosed)
	})
	c.pool.Close()
}

// isClosed reports the closed mark new operations gate on.
func (c *Client) isClosed() bool { return c.closed.Load() }

// Mode reports the internally selected adapter route. It is derived from the
// configured source count and cannot be changed by callers.
func (c *Client) Mode() rpc.CorroborationMode { return c.corrob.Snapshot().Mode }

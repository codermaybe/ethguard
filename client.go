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

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/aggregator"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

// Client is EthGuard's only client: typed reads with ethclient-shaped
// signatures, the generic Call decision entry, and the write broadcast.
// Single-source reads are Forwarded; multi-source reads follow each method's
// corroboration contract. Multiple goroutines may use one Client concurrently;
// connections are owned and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	// plans are the read methods driven by explicit execution plans; their
	// Normalize capability supplies standalone comparison keys.
	plans map[string]rpcengine.ReadPlan
	// reference is the runtime-owned anchor establishment service; the facade
	// only queries it (AnchorBlockHash and friends), never replaces it.
	reference *runtime.ReferenceService
	// corrob is the construction-time adjudication rule; callers do not
	// configure its mode or support floor.
	corrob rpcengine.Corroboration
	// collectTimeout bounds each write and diagnostic fan-out; the read aggregator owns its
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
	// concurrent and repeated calls share the same complete close barrier.
	closeOnce sync.Once
	// closed is the closed mark new operations gate on.
	closed         atomic.Bool
	subscriptionMu sync.Mutex
	subscriptions  map[*attachedSubscription]struct{}
}

// Option tunes a Client at construction time.
type Option func(*clientOptions)

type clientOptions struct {
	logger         *slog.Logger
	recorder       observe.Recorder
	tipEnabled     bool
	tipInterval    time.Duration
	tipMaxBehind   uint64
	collectTimeout time.Duration
	// anchoring carries the business-read establishment strategy: the
	// finalized preset selects the candidate-selection algorithm with its
	// lag bound. Compose also selects the standalone service's rule.
	useFinalizedAnchoring bool
	finalizedMaxLag       uint64
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

// WithCollectTimeout bounds each aggregate collection, diagnostic sample, or
// forwarded attempt.
// A non-positive timeout panics: an Option cannot return an error, and a
// silently ignored timeout would trade a loud construction failure for
// unbounded collections.
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
// similar knobs. Construction rejects this preset with fewer than two sources.
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
// config's single source of defaults (decision 0004). A single-source client
// retains periodic health and RTT sampling but has no peer freshness evidence.
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
// Empty or duplicate upstreams are rejected. One upstream selects Forwarded
// mode; two or more select Majority mode. Dialing each source is deferred until
// its first call. The finalized anchoring preset requires multiple upstreams.
//
// Precedence between the two construction paths: an explicit Go option
// always wins over a value derived from the configuration, and a zero
// option value falls through to the documented default. Duration strings
// in the configuration are validated by config.Normalize before options are
// applied, so the parses below cannot fail.
func New(cfg config.Config, opts ...Option) (*Client, error) {
	normalized, err := config.Normalize(cfg)
	if err != nil {
		return nil, err
	}
	cfg = normalized
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
		reference:      runtimeBundle.Reference,
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
	return plan.Normalize(raw)
}

// Close stops the client: it marks the client closed, cancels the owner
// lifecycle (in-flight operations converge — their merged contexts carry a
// recognizable ErrPoolClosed cause), and closes the shared pool. New
// operations fail with ErrPoolClosed before any network contact. Results
// already delivered to a caller are never rewritten.
//
// It is safe to call from any number of goroutines: the mark and the
// cancellation happen exactly once, and every caller — first or concurrent —
// waits for the pool and every attached subscription delivery worker. It
// does not wait for callers to finish processing already received results.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.subscriptionMu.Lock()
		c.closed.Store(true)
		attached := make([]*attachedSubscription, 0, len(c.subscriptions))
		for entry := range c.subscriptions {
			attached = append(attached, entry)
		}
		c.subscriptions = nil
		c.subscriptionMu.Unlock()
		c.cancelLife(pool.ErrClosed)
		for _, entry := range attached {
			entry.life.Stop()
		}
		c.pool.Close()
		for _, entry := range attached {
			<-entry.done
		}
	})
}

// isClosed reports the closed mark new operations gate on.
func (c *Client) isClosed() bool { return c.closed.Load() }

// Mode reports the internally selected adapter route. It is derived from the
// configured source count and cannot be changed by callers.
func (c *Client) Mode() rpc.CorroborationMode { return c.corrob.Mode }

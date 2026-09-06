package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ethguard/aggregator"
	"ethguard/config"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Client is the advanced entry point for generic calls and decision evidence.
// Prefer ethadapter.Client for ethclient-shaped application reads.
// Multiple goroutines may use one Client concurrently; connections are owned
// and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	policies   rpc.Registry
	resolver   AnchorResolver
}

// Option tunes a Client at construction time.
type Option func(*clientOptions)

type clientOptions struct {
	logger         *slog.Logger
	recorder       observe.Recorder
	anchor         AnchorResolver
	anchorCache    time.Duration
	quorum         int
	tipEnabled     bool
	tipInterval    time.Duration
	tipMaxBehind   uint64
	collectTimeout time.Duration
	anchorFactory  func(*pool.Pool, observe.Recorder) AnchorResolver
	poolOpts       []pool.PoolOption
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

// WithAnchorResolver replaces the strategy establishing verified block
// references behind moving-tag reads (see AnchorResolver). It wins over the
// anchoring presets; the default is strict consensus on the latest head.
func WithAnchorResolver(r AnchorResolver) Option {
	return func(o *clientOptions) { o.anchor = r }
}

// WithQuorum applies the strict quorum floor to every strict method and to
// strict head anchoring. Zero is reserved for the default full-agreement mode.
func WithQuorum(n int) Option {
	if n <= 0 {
		panic("ethguard: WithQuorum: n must be positive")
	}
	return func(o *clientOptions) { o.quorum = n }
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
// across sources (NewFinalizedHeadAnchor), so divergent latest tips no
// longer fail anchored reads. Options tune the freshness bound and similar
// knobs.
func WithFinalizedAnchoring(opts ...FinalizedAnchorOption) Option {
	return func(o *clientOptions) {
		o.anchorFactory = func(p *pool.Pool, rec observe.Recorder) AnchorResolver {
			return NewFinalizedHeadAnchor(p, rec, opts...)
		}
	}
}

// WithAnchorCaching reuses the established block reference for ttl across
// moving-tag reads, so an anchored call costs one fan-out instead of two
// (NewCachedAnchor). Failures are never cached. Size ttl against freshness
// needs: it bounds how far behind the chain a cached reference may lag.
func WithAnchorCaching(ttl time.Duration) Option {
	return func(o *clientOptions) { o.anchorCache = ttl }
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
	if cfg.Quorum > 0 {
		co.quorum = cfg.Quorum
	}
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
		co.anchorFactory = func(p *pool.Pool, rec observe.Recorder) AnchorResolver {
			return NewFinalizedHeadAnchor(p, rec, WithMaxFinalizedLag(maxLag))
		}
	}
	for _, opt := range opts {
		opt(&co)
	}
	if co.tipEnabled {
		co.poolOpts = append(co.poolOpts, pool.WithTipTracking(co.tipInterval, co.tipMaxBehind))
	}
	rec := co.recorder
	if rec == nil && co.logger != nil {
		rec = observe.NewLog(co.logger)
	}
	if rec == nil {
		rec = observe.Nop
	}

	rpcPool := pool.NewPoolWithOptions(cfg.ChainID, co.poolOpts...)
	for _, upstream := range cfg.Upstreams {
		if _, err := rpcPool.Join(upstream.ID, upstream.Endpoint); err != nil {
			return nil, fmt.Errorf("ethguard: register %q: %w", upstream.Endpoint, err)
		}
	}

	resolver := co.anchor
	var strictOpts []rpc.StrictOption
	if co.quorum > 0 {
		strictOpts = append(strictOpts, rpc.WithQuorum(co.quorum))
	}
	if resolver == nil {
		if co.anchorFactory != nil {
			resolver = co.anchorFactory(rpcPool, rec)
		} else {
			resolver = newStrictHeadAnchor(rpcPool, rec, co.collectTimeout, strictOpts...)
		}
	}
	if finalized, ok := resolver.(*FinalizedHeadAnchor); ok && co.anchor == nil {
		finalized.quorum = co.quorum
		finalized.timeout = co.collectTimeout
	}
	if co.anchor != nil {
		resolver = &confirmedAnchor{inner: co.anchor, pool: rpcPool, rec: rec, quorum: co.quorum, timeout: co.collectTimeout}
	}
	if co.anchorCache > 0 {
		resolver = NewCachedAnchor(resolver, co.anchorCache)
	}
	policies := rpc.Registry{
		methods.MethodChainID:               methods.ChainIDPolicy(strictOpts...),
		methods.MethodGetBlockByHash:        methods.BlockByHashPolicy(strictOpts...),
		methods.MethodGetBlockByNumber:      methods.BlockByNumberPolicy(strictOpts...),
		methods.MethodGetBalance:            methods.GetBalancePolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodGetTransactionReceipt: methods.TransactionReceiptPolicy(strictOpts...),
		methods.MethodGetTransactionByHash:  methods.TransactionByHashPolicy(strictOpts...),
		methods.MethodGetBlockReceipts:      methods.BlockReceiptsPolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodGetTransactionCount:   methods.GetTransactionCountPolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodGetStorageAt:          methods.GetStorageAtPolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodGetCode:               methods.GetCodePolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodCall:                  methods.CallPolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodEstimateGas:           methods.EstimateGasPolicy(anchorHashOf(resolver)),
		methods.MethodGetLogs:               methods.LogsPolicy(anchorHeightOf(resolver), strictOpts...),
		methods.MethodFeeHistory:            methods.FeeHistoryPolicy(anchorHeightOf(resolver), strictOpts...),
		methods.MethodGasPrice:              methods.GasPricePolicy(),
		methods.MethodMaxPriorityFeePerGas:  methods.MaxPriorityFeePerGasPolicy(),
		methods.MethodPeerCount:             methods.PeerCountPolicy(),
		methods.MethodSyncing:               methods.SyncingPolicy(),
		methods.MethodNetVersion:            methods.NetVersionPolicy(strictOpts...),
		methods.MethodListening:             methods.ListeningPolicy(),
	}
	return &Client{
		pool:       rpcPool,
		aggregator: aggregator.New(rpcPool, policies, aggregator.WithRecorder(rec), aggregator.WithCollectTimeout(co.collectTimeout)),
		policies:   policies,
		resolver:   resolver,
	}, nil
}

// AnchorBlockHash returns the currently verified block reference behind
// moving-tag reads, establishing it through the configured AnchorResolver
// strategy if necessary (and consulting its cache when caching is enabled).
func (c *Client) AnchorBlockHash(ctx context.Context) (string, error) {
	return c.resolver.AnchorBlockHash(ctx)
}

// AnchorBlockHeight returns the height of the currently verified block
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

// Normalize reduces one raw response through the owning method policy's
// canonical form — the single owner of value parsing for a verified method.
// Typed callers use it instead of re-parsing raw JSON.
func (c *Client) Normalize(method string, raw json.RawMessage) (string, error) {
	return c.policies.PolicyFor(method).Normalize(raw)
}

// Pool exposes the underlying multi-source manager for advanced use.
// Prefer Client methods for normal application code.
func (c *Client) Pool() *pool.Pool {
	return c.pool
}

// Close releases all upstream connections owned by the client.
// It is safe to call more than once.
func (c *Client) Close() {
	c.pool.Close()
}

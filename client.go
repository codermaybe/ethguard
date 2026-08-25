package ethguard

import (
	"fmt"
	"log/slog"

	"ethguard/aggregator"
	"ethguard/config"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Client is the single entry point for application code.
// Multiple goroutines may use one Client concurrently; connections are owned
// and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	policies   rpc.Registry
}

// Option tunes a Client at construction time.
type Option func(*clientOptions)

type clientOptions struct {
	logger        *slog.Logger
	recorder      observe.Recorder
	anchor        AnchorResolver
	anchorFactory func(*pool.Pool, observe.Recorder) AnchorResolver
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

// New builds a Client from an already-loaded configuration.
// Upstreams are registered into the RPC pool with endpoint deduplication.
// Dialing each source is deferred until its first call.
func New(cfg config.Config, opts ...Option) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var co clientOptions
	for _, opt := range opts {
		opt(&co)
	}
	rec := co.recorder
	if rec == nil && co.logger != nil {
		rec = observe.NewLog(co.logger)
	}
	if rec == nil {
		rec = observe.Nop
	}

	rpcPool := pool.NewPoolWithChainID(cfg.ChainID)
	for _, upstream := range cfg.Upstreams {
		if _, err := rpcPool.Join(upstream.ID, upstream.Endpoint); err != nil {
			return nil, fmt.Errorf("ethguard: register %q: %w", upstream.Endpoint, err)
		}
	}

	resolver := co.anchor
	if resolver == nil {
		if co.anchorFactory != nil {
			resolver = co.anchorFactory(rpcPool, rec)
		} else {
			resolver = NewStrictHeadAnchor(rpcPool, rec)
		}
	}
	policies := rpc.Registry{
		methods.MethodChainID:          methods.ChainIDPolicy(),
		methods.MethodGetBlockByHash:   methods.BlockByHashPolicy(),
		methods.MethodGetBlockByNumber: methods.BlockByNumberPolicy(),
		methods.MethodGetBalance:       methods.GetBalancePolicy(resolver.AnchorBlockHash),
	}
	return &Client{
		pool:       rpcPool,
		aggregator: aggregator.New(rpcPool, policies, aggregator.WithRecorder(rec)),
		policies:   policies,
	}, nil
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

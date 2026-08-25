package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ethguard/aggregator"
	"ethguard/config"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// anchorMethod labels observability events for anchored reads establishing
// their block reference. A fixed literal keeps label cardinality bounded.
const anchorMethod = "anchor"

// Client is the single entry point for application code.
// Multiple goroutines may use one Client concurrently; connections are owned
// and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	policies   rpc.Registry
	rec        observe.Recorder
}

// Option tunes a Client at construction time.
type Option func(*clientOptions)

type clientOptions struct {
	logger   *slog.Logger
	recorder observe.Recorder
}

// WithLogger injects a structured logger receiving one event per aggregate
// decision (status, conflict, fault count, per-source RTT). It is ignored
// when WithRecorder supplies an explicit sink.
func WithLogger(l *slog.Logger) Option {
	return func(o *clientOptions) { o.logger = l }
}

// WithRecorder injects a custom observability sink, such as the collector
// from ethguard/observe/prometheus.
func WithRecorder(r observe.Recorder) Option {
	return func(o *clientOptions) { o.recorder = r }
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

	// Anchored policies resolve their block reference through the same pool,
	// before the registry exists — hence the closure over the pool only.
	anchor := func(ctx context.Context) (string, error) {
		return anchorLatestBlock(ctx, rec, rpcPool)
	}

	policies := rpc.Registry{
		methods.MethodChainID:          methods.ChainIDPolicy(),
		methods.MethodGetBlockByHash:   methods.BlockByHashPolicy(),
		methods.MethodGetBlockByNumber: methods.BlockByNumberPolicy(),
		methods.MethodGetBalance:       methods.GetBalancePolicy(anchor),
	}
	return &Client{
		pool:       rpcPool,
		aggregator: aggregator.New(rpcPool, policies, aggregator.WithRecorder(rec)),
		policies:   policies,
		rec:        rec,
	}, nil
}

// anchorLatestBlock establishes the anchor for moving-tag reads: fan out a
// latest-block query and accept it only under strict cross-source agreement.
// The block head policy is itself unanchored, so this cannot recurse. If
// sources sit on different heads the call fails with conflict evidence rather
// than guessing which head is right; conservative selection over divergent
// heads (common ancestor, lowest safe/finalized) is deliberately future work.
func anchorLatestBlock(ctx context.Context, rec observe.Recorder, rpcPool *pool.Pool) (string, error) {
	started := time.Now()
	emissions, collectErr := rpcPool.Collect(ctx,
		methods.MethodGetBlockByNumber, []any{"latest", false}, pool.CollectOptions{})
	if collectErr != nil {
		rec.RecordDecision(observe.BuildDecision(started, anchorMethod, rpc.Decision{}, collectErr, emissions))
		return "", fmt.Errorf("anchor: collect: %w", collectErr)
	}
	observations := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{Source: emission.Src, Value: emission.Val, Err: emission.Err}
	}
	headPolicy := methods.BlockByNumberPolicy()
	decision, err := headPolicy.Resolve(observations)
	rec.RecordDecision(observe.BuildDecision(started, anchorMethod, decision, err, emissions))
	if err != nil {
		return "", fmt.Errorf("anchor: %w", err)
	}
	var head struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(decision.Value, &head); err != nil || len(head.Hash) < 3 {
		return "", fmt.Errorf("anchor: winning block carries no hash")
	}
	return head.Hash, nil
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

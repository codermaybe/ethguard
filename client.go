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
	// corrob is the shared, runtime-mutable adjudication configuration
	// behind SetCorroboration; every invocation snapshots it at its start.
	corrob *rpc.CorroborationShared
	// collectTimeout bounds each write fan-out; the read aggregator owns its
	// own copy supplied at construction.
	collectTimeout time.Duration
}

// Option tunes a Client at construction time.
type Option func(*clientOptions)

type clientOptions struct {
	logger         *slog.Logger
	recorder       observe.Recorder
	anchor         AnchorResolver
	anchorSet      bool
	anchorCache    time.Duration
	quorum         int
	quorumSet      bool
	corroboration  rpc.CorroborationMode
	modeSet        bool
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

// WithAnchorResolver is retained only as a migration diagnostic. Decision
// 0036 makes reference establishment library-owned; supplying this legacy
// injection causes New to return a configuration error.
func WithAnchorResolver(r AnchorResolver) Option {
	return func(o *clientOptions) {
		if r != nil {
			o.anchor, o.anchorSet = r, true
		}
	}
}

// WithQuorum sets the corroboration support floor H for every strict method
// and strict head anchoring (decision 0029). Callers relating H to their
// fault assumptions should compare it against config.DefaultSupportFloor. A
// non-positive n is a configuration error.
func WithQuorum(n int) Option {
	if n <= 0 {
		panic("ethguard: WithQuorum: n must be positive")
	}
	return func(o *clientOptions) { o.quorum, o.quorumSet = n, true }
}

// WithCorroborationMode selects the adjudication mode for every corroborated
// read (decision 0028): fixed-membership majority by default, or the explicit
// unanimity mode with the same support floor. It wins over the YAML
// corroboration preset.
func WithCorroborationMode(m rpc.CorroborationMode) Option {
	return func(o *clientOptions) { o.corroboration, o.modeSet = m, true }
}

// WithForwardingMode selects intentional single-source forwarding. It is
// useful for an explicitly trusted one-member deployment and never occurs as
// an automatic response to health or membership changes.
func WithForwardingMode() Option {
	return WithCorroborationMode(rpc.CorroborationForwarded)
}

// SetCorroboration atomically changes the adjudication mode and support floor
// for invocations that start after this call returns. An invocation already
// in flight keeps the mode, floor, and voting basis captured at its start;
// configuration never changes mid-invocation (decisions 0025 and 0027).
// Membership changes are separate, explicit pool operations.
func (c *Client) SetCorroboration(mode rpc.CorroborationMode, floor int) error {
	return c.corrob.Set(mode, floor)
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

// WithAnchorCaching is retained only to produce an explicit migration error.
// Cross-call reference caching is retired by decision 0037; a zero value is
// disabled and non-zero values are rejected by New.
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
	co := clientOptions{collectTimeout: config.DefaultCollectTimeout(), corroboration: rpc.CorroborationMajority}
	if cfg.Quorum > 0 {
		co.quorum = cfg.Quorum
	}
	// The preset comparison mirrors config.Validate's normalization exactly:
	// a case-spelling that passes validation must select the same mode here,
	// or the YAML path silently changes adjudication behavior (decision
	// 0004's failure mode).
	if cfg.Corroboration != "" {
		switch strings.ToLower(strings.TrimSpace(cfg.Corroboration)) {
		case "majority":
			co.corroboration = rpc.CorroborationMajority
		case "unanimity":
			co.corroboration = rpc.CorroborationUnanimity
		case "forwarded":
			co.corroboration = rpc.CorroborationForwarded
		default:
			return nil, fmt.Errorf("ethguard: corroboration must be majority or unanimity (or forwarded), got %q", cfg.Corroboration)
		}
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
	if co.anchorCache != 0 {
		return nil, fmt.Errorf("ethguard: anchor_cache_ttl is no longer supported; non-zero duration %s conflicts with decision 0026", co.anchorCache)
	}
	if co.anchorSet {
		return nil, fmt.Errorf("ethguard: WithAnchorResolver is retired; reference establishment is library-owned under decision 0036")
	}
	explicitSingleMode := cfg.Corroboration != "" || cfg.Quorum > 0 || co.modeSet || co.quorumSet
	if len(cfg.Upstreams) == 1 && !explicitSingleMode && co.corroboration == rpc.CorroborationMajority {
		co.corroboration = rpc.CorroborationForwarded
	}
	if len(cfg.Upstreams) == 1 && explicitSingleMode && co.corroboration != rpc.CorroborationForwarded {
		return nil, fmt.Errorf("ethguard: explicit corroboration mode %s requires at least two configured voting sources", co.corroboration)
	}
	if co.corroboration == rpc.CorroborationForwarded && co.quorum > 0 {
		return nil, fmt.Errorf("ethguard: forwarded mode cannot specify quorum %d", co.quorum)
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

	// One shared corroboration configuration (decision 0025): the floor
	// defaults to the config package's single source, and every invocation
	// snapshots mode and floor before any request is issued.
	floor := co.quorum
	if floor == 0 && co.corroboration != rpc.CorroborationForwarded {
		floor = config.DefaultSupportFloor()
	}
	corrob, err := rpc.NewCorroborationShared(co.corroboration, floor)
	if err != nil {
		return nil, fmt.Errorf("ethguard: %w", err)
	}

	resolver := co.anchor
	var strictOpts []rpc.StrictOption
	// Construction-time defaults mirror the shared configuration so policies
	// resolved outside a snapshot-bearing aggregator agree with the client
	// default. The reference-establishment paths keep their own formulas
	// until T03 aligns them.
	if floor > 0 {
		strictOpts = append(strictOpts, rpc.WithQuorum(floor))
	}
	if co.corroboration == rpc.CorroborationUnanimity {
		strictOpts = append(strictOpts, rpc.WithUnanimity())
	}
	if resolver == nil {
		if co.anchorFactory != nil {
			resolver = co.anchorFactory(rpcPool, rec)
		} else {
			resolver = newStrictHeadAnchor(rpcPool, rec, co.collectTimeout, corrob, strictOpts...)
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
		methods.MethodBlobBaseFee:           methods.BlobBaseFeePolicy(),
		methods.MethodBlockNumber:           methods.BlockNumberPolicy(),
		methods.MethodCreateAccessList:      methods.CreateAccessListPolicy(anchorHashOf(resolver), strictOpts...),
		methods.MethodPeerCount:             methods.PeerCountPolicy(),
		methods.MethodSyncing:               methods.SyncingPolicy(),
		methods.MethodNetVersion:            methods.NetVersionPolicy(strictOpts...),
		methods.MethodListening:             methods.ListeningPolicy(),
	}
	if err := methods.ValidateRegistry(policies); err != nil {
		return nil, fmt.Errorf("ethguard: %w", err)
	}
	return &Client{
		pool: rpcPool,
		aggregator: aggregator.New(rpcPool, policies,
			aggregator.WithRecorder(rec),
			aggregator.WithCollectTimeout(co.collectTimeout),
			aggregator.WithCorroboration(corrob),
			aggregator.WithProjectors(map[string]rpc.ObservationProjector{
				methods.MethodGetTransactionReceipt: methods.NewStandardProjector(methods.TransactionReceiptStandardFields()),
				methods.MethodGetBlockByHash:        methods.NewStandardProjector(methods.BlockStandardFields()),
				methods.MethodGetBlockByNumber:      methods.NewStandardProjector(methods.BlockStandardFields()),
				methods.MethodGetTransactionByHash:  methods.NewStandardProjector(methods.TransactionStandardFields()),
				methods.MethodGetBalance:            methods.ScalarStandardProjector(),
				methods.MethodCall:                  methods.ScalarStandardProjector(),
			})),
		policies:       policies,
		resolver:       resolver,
		corrob:         corrob,
		collectTimeout: co.collectTimeout,
	}, nil
}

// Corroboration returns the currently configured invocation mode and support
// floor. A forwarded snapshot has no corroboration requirement and reports a
// zero floor.
func (c *Client) Corroboration() rpc.Corroboration { return c.corrob.Snapshot() }

// Mode is a concise startup/runtime visibility hook for operators.
func (c *Client) Mode() rpc.CorroborationMode { return c.corrob.Snapshot().Mode }

// VotingMembers reports the currently configured membership. It is a
// diagnostic snapshot; an invocation already in flight keeps its own basis.
func (c *Client) VotingMembers() int { return c.pool.Len() }

// AnchorBlockHash returns a newly established verified block reference behind
// moving-tag reads through the library-owned anchor strategy.
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

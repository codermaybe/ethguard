package runtime

// The production composition root. The ethguard facade parses business
// options; this package assembles them into the running execution graph: the
// source pool with its transport dialers, the library-owned corroboration
// policy, the reference establishment resolver, the method catalog, and the
// aggregate execution pipeline. Concrete transport knowledge is deliberately
// owned here, so no other package links a concrete transport.

import (
	"fmt"
	"net/url"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/transport"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/transport/wss"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// Adapter names the connection adapter a composed pool dials upstreams with.
// AdapterNative selects the project-owned HTTP adapter and is the strategic
// default; AdapterGeth names the go-ethereum compatibility adapter, which the
// default build does not link — a bare selection fails composition with a
// pointer to ethguard/gethcompat, the explicit construction entry for that
// channel (gethcompat composes this selection with its own dialer).
type Adapter string

const (
	AdapterGeth   Adapter = "geth"
	AdapterNative Adapter = "native"
)

// TipTracking enables pool-side block-tip sampling and the freshness
// eligibility gate. Zero values select the documented defaults (10s sampling,
// 32-block bound), applied by pool from config's single source of defaults
// (decision 0004).
type TipTracking struct {
	Interval  time.Duration
	MaxBehind uint64
}

// Composition is the business configuration one client runtime is composed
// from: the source fleet with its adapter selection, the freshness sampler,
// the observability sink, and the establishment strategy.
type Composition struct {
	// ChainID optionally constrains upstreams to one Ethereum network. Zero
	// leaves chain identity validation to the pool.
	ChainID uint64
	// Upstreams is the configured source fleet, already validated by the
	// configuration layer.
	Upstreams []config.Upstream
	// Adapter selects the connection adapter for sources without an explicit
	// per-source transport. Empty keeps the strategic native default.
	Adapter Adapter
	// ExplicitDialer installs a connection adapter dialer for the whole pool
	// (the gethcompat construction seam). Sources with an explicit transport
	// conflict with it.
	ExplicitDialer transport.Dialer
	// TipTracking enables the freshness sampler when non-nil.
	TipTracking *TipTracking
	// Recorder receives one event per aggregate decision. Nil degrades to a
	// silent sink — the facade resolves its logger fallback before composing.
	Recorder observe.Recorder
	// CollectTimeout bounds each aggregate collection and write fan-out.
	CollectTimeout time.Duration
	// Anchoring is nil for the strict-head strategy; the finalized preset
	// passes the strategy with its lag bound.
	Anchoring *AnchoringStrategy
}

// Compose assembles one client runtime from business configuration: the
// source pool with its transport dialers, the library-owned corroboration
// policy (majority with a support floor of two, forwarded with no floor for a
// single-source fleet — decisions 0029 and 0030), the reference establishment
// resolver, the method catalog, and the aggregate execution pipeline. Dialing
// each source is deferred until its first call.
func Compose(in Composition) (*Bundle, error) {
	poolOpts := make([]pool.PoolOption, 0, 2)
	if in.TipTracking != nil {
		poolOpts = append(poolOpts, pool.WithTipTracking(in.TipTracking.Interval, in.TipTracking.MaxBehind))
	}
	switch in.Adapter {
	case "", AdapterNative:
		// Strategic default: the project-owned native HTTP adapter (the
		// transport contract's normative implementation). An explicit dialer
		// injected through the gethcompat seam wins.
		if in.ExplicitDialer == nil {
			poolOpts = append(poolOpts, pool.WithDialer(nativehttp.NewDialer()))
		}
	case AdapterGeth:
		// The go-ethereum compatibility adapter is not linked into the
		// default build. With an explicitly injected dialer (the seam's own
		// dialer), the selection composes; a bare selection fails with a
		// pointer to ethguard/gethcompat.
		if in.ExplicitDialer == nil {
			return nil, fmt.Errorf("the %q adapter is not linked into the default build; construct through ethguard/gethcompat", AdapterGeth)
		}
	default:
		return nil, fmt.Errorf("unknown HTTP adapter %q (want %q or %q)", in.Adapter, AdapterGeth, AdapterNative)
	}
	if in.ExplicitDialer != nil {
		poolOpts = append(poolOpts, pool.WithDialer(in.ExplicitDialer))
	}

	members := make([]pool.Member, 0, len(in.Upstreams))
	for _, upstream := range in.Upstreams {
		member := pool.Member{Name: upstream.ID, URL: upstream.Endpoint}
		if upstream.Transport == "" && in.ExplicitDialer == nil {
			if endpoint, parseErr := url.Parse(upstream.Endpoint); parseErr == nil && (endpoint.Scheme == "ws" || endpoint.Scheme == "wss") {
				return nil, fmt.Errorf("source %q: websocket endpoint requires transport: wss", upstream.ID)
			}
		}
		if upstream.Transport != "" && in.ExplicitDialer != nil {
			return nil, fmt.Errorf("source %q: explicit transport conflicts with injected dialer", upstream.ID)
		}
		switch upstream.Transport {
		case config.TransportNativeHTTP:
			member.Dialer = nativehttp.NewDialer()
		case config.TransportWSS:
			member.Dialer = wss.NewDialer()
		}
		members = append(members, member)
	}
	rpcPool, err := pool.NewWithMembers(in.ChainID, members, poolOpts...)
	if err != nil {
		return nil, fmt.Errorf("register upstreams: %w", err)
	}
	// The voting basis must stay exactly what the configuration declared:
	// pool folds spelling variants of one endpoint (case, default port,
	// trailing slash) into one member, and a silent fold would shrink the
	// basis below the operator's declared trust set. Config rejects
	// normalized duplicates first; this check is the composition-side
	// backstop against drift between the two identity rules.
	if got, want := rpcPool.Len(), len(in.Upstreams); got != want {
		return nil, fmt.Errorf("register upstreams: %d configured upstreams collapsed to %d pool members: remove duplicate endpoints after normalization (scheme/host case, default ports, trailing slash)", want, got)
	}

	// The library owns the corroboration policy. It snapshots the fixed basis
	// for each invocation and derives the support threshold internally.
	mode := rpc.CorroborationMajority
	if len(in.Upstreams) == 1 {
		mode = rpc.CorroborationForwarded
	}
	floor := 2
	if mode == rpc.CorroborationForwarded {
		floor = 0
	}
	corrob, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		return nil, err
	}

	// Construction-time defaults mirror the shared configuration so policies
	// resolved outside a snapshot-bearing aggregator agree with the client
	// default.
	var strictOpts []rpc.StrictOption
	if floor > 0 {
		strictOpts = append(strictOpts, rpc.WithQuorum(floor))
	}
	bundle, err := Build(Inputs{
		Pool:           rpcPool,
		StrictOptions:  strictOpts,
		Recorder:       in.Recorder,
		CollectTimeout: in.CollectTimeout,
		Corroboration:  corrob,
		Anchoring:      in.Anchoring,
	})
	if err != nil {
		return nil, err
	}
	resolver := composeResolver(in, bundle.Stage, corrob, floor)
	bundle.Pool = rpcPool
	bundle.Corroboration = corrob
	bundle.Resolver = resolver
	return bundle, nil
}

// composeResolver selects the reference resolver for the composed runtime
// over the one shared stage: the finalized preset hands the stage's
// candidate-selection strategy to the finalized service; the default hands
// the stage's strict head rounds to the strict head service. The facade and
// the invocation's business reads therefore establish through one
// establishment with one configuration.
func composeResolver(in Composition, stage *ReferenceStage, corrob *rpc.CorroborationShared, floor int) ReferenceResolver {
	if in.Anchoring != nil && in.Anchoring.Finalized {
		return &FinalizedHeadAnchor{service: NewFinalizedHeadService(stage)}
	}
	return &StrictHeadAnchor{service: NewStrictHeadService(stage, corrob)}
}

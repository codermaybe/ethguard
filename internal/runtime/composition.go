package runtime

// The production composition root. The ethguard facade parses business
// options; this package assembles them into the running execution graph: the
// source pool with its transport dialers, the library-owned corroboration
// policy, the reference establishment resolver, the method catalog, and the
// aggregate execution pipeline. Concrete transport knowledge is deliberately
// owned here, so no other package links a concrete transport.

import (
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/endpoint"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/transport/ipc"
	"github.com/codermaybe/ethguard/internal/transport/wss"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
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
// from: the source fleet with its per-source transport, the freshness sampler,
// the observability sink, and the establishment strategy.
type Composition struct {
	// ChainID optionally constrains upstreams to one Ethereum network. Zero
	// leaves chain identity validation to the pool.
	ChainID uint64
	// Upstreams is the configured source fleet, already validated by the
	// configuration layer.
	Upstreams []config.Upstream
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
	if len(in.Upstreams) == 0 {
		return nil, fmt.Errorf("at least one upstream is required")
	}
	if len(in.Upstreams) == 1 && in.Anchoring != nil && in.Anchoring.Finalized {
		return nil, fmt.Errorf("finalized anchoring requires at least two distinct upstreams: %w", rpc.ErrInvalidRequest)
	}
	normalized, err := config.Normalize(config.Config{Upstreams: in.Upstreams})
	if err != nil {
		return nil, err
	}
	in.Upstreams = normalized.Upstreams
	poolOpts := make([]pool.PoolOption, 0, 2)
	if in.TipTracking != nil {
		poolOpts = append(poolOpts, pool.WithTipTracking(in.TipTracking.Interval, in.TipTracking.MaxBehind))
	}
	poolOpts = append(poolOpts, pool.WithDialer(nativehttp.NewDialer()))

	members := make([]pool.Member, 0, len(in.Upstreams))
	for _, upstream := range in.Upstreams {
		parsed, parseErr := endpoint.Parse(upstream.Endpoint)
		if parseErr != nil {
			return nil, fmt.Errorf("source %q: %w", upstream.ID, parseErr)
		}
		member := pool.Member{Name: upstream.ID, Endpoint: parsed}
		switch upstream.Transport {
		case config.TransportNativeHTTP:
			member.Dialer = nativehttp.NewDialer()
		case config.TransportWSS:
			member.Dialer = wss.NewDialer()
		case config.TransportIPC:
			member.Dialer = ipc.NewDialer()
		default:
			return nil, fmt.Errorf("source %q: unsupported transport %q", upstream.ID, upstream.Transport)
		}
		members = append(members, member)
	}
	rpcPool, err := pool.NewWithMembers(in.ChainID, members, poolOpts...)
	if err != nil {
		return nil, fmt.Errorf("register upstreams: %w", err)
	}
	composed := false
	defer func() {
		if !composed {
			rpcPool.Close()
		}
	}()
	// The voting basis must stay exactly what the configuration declared:
	// pool folds spelling variants of one endpoint (case, default port,
	// trailing slash) into one member, and a silent fold would shrink the
	// basis below the operator's declared trust set. Config rejects
	// normalized duplicates first; this check is the composition-side
	// backstop for the declared voting basis.
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
	corrob, err := rpcengine.NewCorroboration(mode, floor)
	if err != nil {
		return nil, err
	}

	bundle, err := Build(Inputs{
		Pool:           rpcPool,
		Recorder:       in.Recorder,
		CollectTimeout: in.CollectTimeout,
		Corroboration:  corrob,
		Anchoring:      in.Anchoring,
	})
	if err != nil {
		return nil, err
	}
	referenceRule := corrob
	if in.Anchoring != nil && in.Anchoring.Finalized {
		// Standalone finalized queries retain the preset's majority floor,
		// including strict safe queries.
		referenceRule = rpcengine.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	}
	bundle.Pool = rpcPool
	bundle.Reference = NewReferenceService(bundle.Stage, referenceRule)
	composed = true
	return bundle, nil
}

package pool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/config"
)

// BroadcastOption tunes one broadcast fan-out.
type BroadcastOption func(*broadcastConfig)

type broadcastConfig struct {
	timeout time.Duration
	sources []*Source
}

// WithBroadcastTimeout bounds the whole fan-out per attempt round. Zero uses
// the collection default.
func WithBroadcastTimeout(timeout time.Duration) BroadcastOption {
	return func(c *broadcastConfig) { c.timeout = timeout }
}

// WithBroadcastSources fixes the membership for one broadcast invocation.
// The caller owns the snapshot; closed members remain in the returned
// emissions so the write denominator cannot change during the request.
func WithBroadcastSources(sources []*Source) BroadcastOption {
	return func(c *broadcastConfig) {
		if sources == nil {
			c.sources = nil
			return
		}
		c.sources = make([]*Source, len(sources))
		copy(c.sources, sources)
	}
}

// Broadcast sends one idempotent request to every requestable source in
// parallel and collects every source's outcome. The same raw bytes re-sent
// derive the same transaction hash on every honest node, so re-broadcasting
// is safe; what Broadcast never does is short-circuit or gate by read
// freshness. Unlike Collect and CollectVoting it:
//
//   - never applies the tip-freshness gate. A source trailing the fleet tip
//     can still accept a valid transaction; gating a write by read freshness
//     would shrink propagation for no integrity gain.
//   - never short-circuits on a first success. Every eligible source is
//     contacted in one wave so the invocation has the whole fleet's outcome
//     before any verdict.
//
// Chain identity is still verified per source, and a member that fails its
// identity check fails as one member (ErrChainIDMismatch) without pausing
// the others. The returned slice has one Emission per contacted source, in
// pool order; a source skipped by its cooldown circuit carries ErrSourceGated
// with Attempted=false and receives no health penalty, mirroring
// CollectVoting's voting-basis semantics.
//
// The error conventions follow the collection paths: a terminated fan-out
// with nothing usable returns ErrAllSourcesDown (or the identity sentinel),
// while each per-source cause stays on its Emission.
func (p *Pool) Broadcast(ctx context.Context, method string, params []any, opts ...BroadcastOption) ([]Emission, error) {
	cfg := broadcastConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	timeout := cfg.timeout
	if timeout <= 0 {
		timeout = config.DefaultCollectTimeout()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if p.isClosed() {
		return nil, ErrClosed
	}

	// Broadcast contacts every registered source that could accept the
	// request: closed sources are still visible as failed emissions so the
	// caller sees the full membership outcome, while identity-invalid members
	// fail fast without a wire round.
	var members []*Source
	if cfg.sources != nil {
		members = append([]*Source(nil), cfg.sources...)
	} else {
		p.mu.RLock()
		members = make([]*Source, len(p.sources))
		copy(members, p.sources)
		closed := p.closed
		p.mu.RUnlock()
		if closed {
			return nil, ErrClosed
		}
	}
	if len(members) == 0 {
		return nil, ErrNoEligibleSource
	}

	now := time.Now()
	emissions := make([]Emission, len(members))
	gated := make(map[int]error, len(members))
	attemptable := 0
	for i, src := range members {
		switch {
		case src.isClosed():
			emissions[i] = Emission{Src: src, Err: ErrClosed}
		case src.isIdentityInvalid():
			emissions[i] = Emission{Src: src, Err: ErrChainIDMismatch}
			gated[i] = ErrChainIDMismatch
		case !src.requestable(now):
			// A cooling source is paused, not failed: the write may already
			// have landed through the fleet's other members.
			emissions[i] = Emission{Src: src, Err: ErrSourceGated}
			gated[i] = ErrSourceGated
		default:
			attemptable++
		}
	}
	if attemptable == 0 {
		for i, src := range members {
			if emissions[i].Src == nil {
				emissions[i] = Emission{Src: src, Err: gated[i]}
			}
		}
		return emissions, ErrNoEligibleSource
	}

	maxConcurrent := len(members)
	sem := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup
	for i, src := range members {
		if gated[i] != nil || src.isClosed() {
			continue
		}
		dispatchOne(callCtx, ctx, sem, i, src, method, params, emissions, &wg)
	}
	wg.Wait()
	// A laggard source must not turn the whole broadcast into an error while
	// other members produced usable answers: the per-source timeout stays on
	// its own emission (parity with the collection paths' callCtx behavior is
	// deliberately not copied here — a broadcast's verdict needs every
	// emission, and the caller's ctx remains the outer bound).
	for _, emission := range emissions {
		if emission.Val != nil || isJSONRPCResponseError(emission.Err) {
			return emissions, nil
		}
	}
	if err := callCtx.Err(); err != nil {
		return emissions, err
	}
	for _, emission := range emissions {
		if errors.Is(emission.Err, ErrChainIDMismatch) {
			return emissions, emission.Err
		}
	}
	return emissions, ErrAllSourcesDown
}

package pool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

// Emission is the result of a single upstream fan-out request.
// It carries the raw, unjudged response (or error) from one source; semantic
// comparison is owned by the aggregator, not the pool.
type Emission struct {
	// Src is the source that answered (or failed).
	Src *Source
	// Val is the raw RPC response value. Only set when Err == nil.
	Val json.RawMessage
	// Err reports this source's transport/JSON-RPC-level failure or timeout.
	// A non-nil Err means this source did not contribute a value.
	Err error
	// RTT is the round-trip time of the attempted call; zero when the call
	// never started or failed before reaching the wire.
	RTT time.Duration
	// Attempted reports whether the call was actually issued to this source.
	// False means the fan-out expired while this source waited for a slot;
	// such an emission carries no observation and must not count as one.
	Attempted bool
}

// CollectOptions configures one fan-out. Zero value is a sensible default.
type CollectOptions struct {
	// SourceIDs fixes membership for a continuation of a previous collection.
	// Nil selects current eligible sources. Explicit IDs retain pool order and
	// bypass changing freshness/cooldown gates; identity and lifecycle checks
	// still apply. Unknown or duplicate IDs are rejected.
	SourceIDs []string
	// Timeout bounds the whole fan-out. Zero uses a conservative default.
	Timeout time.Duration
	// MaxConcurrent caps simultaneous in-flight per-source calls.
	// Zero uses the number of eligible sources (unbounded symmetric fan-out).
	MaxConcurrent int
}

// maxDefaultTimeout is applied when CollectOptions.Timeout is zero.
const maxDefaultTimeout = 30 * time.Second

// Collect fans a single request out to all eligible sources symmetrically and
// returns each source's raw response, unjudged.
//
// Concurrency model: eligible sources are scheduled independently under one
// shared context. A single source's failure does not affect the others. Sources
// whose calls actually start report health automatically; expiration while
// waiting for a local concurrency slot does not count against a source.
//
// The returned slice has one Emission per eligible source, in pool order.
func (p *Pool) Collect(ctx context.Context, method string, params []any, opts CollectOptions) ([]Emission, error) {
	return p.collect(ctx, method, params, opts, collectPlan{applyFreshness: true})
}

// collectPlan selects which eligibility rules one collection applies. User
// traffic uses the zero-plus-freshness plan; tip probes relax gates so they
// can observe and recover the fleet.
type collectPlan struct {
	// applyFreshness excludes sources trailing the fleet's tip reference.
	applyFreshness bool
	// warmOnly skips sources without an established connection instead of
	// dialing them, keeping first contact caller-driven. It also bypasses
	// per-source cooldown circuits: a probe verifies recovery proactively.
	warmOnly bool
}

// collect is Collect with a switch for the eligibility rules; tip probes use
// a relaxed plan so a stale or cooling fleet can still be observed.
func (p *Pool) collect(ctx context.Context, method string, params []any, opts CollectOptions, plan collectPlan) ([]Emission, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = maxDefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var sources []*Source
	var err error
	if opts.SourceIDs != nil {
		sources, err = p.sourcesByIDs(opts.SourceIDs)
	} else {
		sources, err = p.sourcesForCollect(plan)
	}
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, ErrNoEligibleSource
	}
	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 || maxConcurrent > len(sources) {
		maxConcurrent = len(sources)
	}
	sem := make(chan struct{}, maxConcurrent)

	var (
		emissions = make([]Emission, len(sources))
		wg        sync.WaitGroup
	)

	for i, src := range sources {
		wg.Add(1)
		go func(i int, src *Source) {
			defer wg.Done()

			var val json.RawMessage
			var err error
			var rtt time.Duration
			attempted := false
			select {
			case sem <- struct{}{}:
				if err = callCtx.Err(); err == nil {
					attempted = true
					start := time.Now()
					val, err = collectOne(callCtx, src, method, params)
					rtt = time.Since(start)
				}
				<-sem
			case <-callCtx.Done():
				err = callCtx.Err()
			}

			// Attempted calls report health here, so callers cannot accidentally
			// hide a dead source by forgetting a separate report step.
			if attempted {
				if isHealthyRPCResponse(err) {
					src.recordSuccessfulCall(rtt)
				} else if shouldRecordFailure(ctx, err) {
					src.recordFailedCall(err)
				}
			}

			emissions[i] = Emission{Src: src, Val: val, Err: err, RTT: rtt, Attempted: attempted}
		}(i, src)
	}
	wg.Wait()
	if err := callCtx.Err(); err != nil {
		return emissions, err
	}

	for _, emission := range emissions {
		if errors.Is(emission.Err, ErrChainIDMismatch) {
			return emissions, emission.Err
		}
		if emission.Val != nil || isJSONRPCResponseError(emission.Err) {
			return emissions, nil
		}
	}
	return emissions, ErrAllSourcesDown
}

// Forward sends a request to one eligible source at a time, in deterministic
// pool order. It fails over only after a transport-level failure. A JSON-RPC
// response error is an answer from that source and stops the sequence.
//
// Unlike Collect, this method deliberately avoids symmetric fan-out for
// unknown, heavy, or non-idempotent methods. The returned emissions contain
// only sources that were actually attempted.
func (p *Pool) Forward(ctx context.Context, method string, params []any, opts CollectOptions) ([]Emission, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = maxDefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var sources []*Source
	var err error
	if opts.SourceIDs != nil {
		sources, err = p.sourcesByIDs(opts.SourceIDs)
	} else {
		sources, err = p.sourcesForCollect(collectPlan{applyFreshness: true})
	}
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, ErrNoEligibleSource
	}

	emissions := make([]Emission, 0, len(sources))
	for _, src := range sources {
		if err := callCtx.Err(); err != nil {
			return emissions, err
		}
		start := time.Now()
		val, callErr := collectOne(callCtx, src, method, params)
		rtt := time.Since(start)
		emission := Emission{Src: src, Val: val, Err: callErr, RTT: rtt, Attempted: true}
		if isHealthyRPCResponse(callErr) {
			src.recordSuccessfulCall(rtt)
		} else if shouldRecordFailure(ctx, callErr) {
			src.recordFailedCall(callErr)
		}
		emissions = append(emissions, emission)

		if callErr == nil || isJSONRPCResponseError(callErr) {
			return emissions, nil
		}
		if errors.Is(callErr, ErrChainIDMismatch) {
			return emissions, callErr
		}
	}
	return emissions, ErrAllSourcesDown
}

func shouldRecordFailure(parent context.Context, err error) bool {
	return err != nil &&
		parent.Err() == nil &&
		!errors.Is(err, ErrClosed) &&
		!errors.Is(err, errWaitingForDial)
}

func isJSONRPCResponseError(err error) bool {
	var rpcErr rpc.Error
	return errors.As(err, &rpcErr)
}

func isHealthyRPCResponse(err error) bool {
	if err == nil {
		return true
	}
	var rpcErr rpc.Error
	if !errors.As(err, &rpcErr) {
		return false
	}
	// These codes mean the peer could not process a valid JSON-RPC exchange.
	return !isProtocolServerError(err) && rpcErr.ErrorCode() != -32700 && rpcErr.ErrorCode() != -32600
}

// collectOne performs a single JSON-RPC call on one source, dialing lazily if
// needed. It returns the raw result value (or an error for transport/timeout).
func collectOne(ctx context.Context, src *Source, method string, params []any) (json.RawMessage, error) {
	client, err := src.clientForCall(ctx)
	if err != nil {
		return nil, err
	}
	if err := src.verifyChainID(ctx, client); err != nil {
		return nil, err
	}

	var result json.RawMessage
	err = client.Client().CallContext(ctx, &result, method, params...)
	if src.isClosed() {
		return nil, ErrClosed
	}
	if err != nil {
		if shouldDiscardClient(err) {
			src.discardClient(client)
		}
		return nil, err
	}
	return result, nil
}

func shouldDiscardClient(err error) bool {
	return errors.Is(err, rpc.ErrClientQuit)
}

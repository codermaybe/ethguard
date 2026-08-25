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
	// RTT is the round-trip time of the attempted call. Zero means the call
	// never started (the fan-out expired while waiting for a slot).
	RTT time.Duration
}

// CollectOptions configures one fan-out. Zero value is a sensible default.
type CollectOptions struct {
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
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = maxDefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sources, err := p.sourcesForCollect()
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

			emissions[i] = Emission{Src: src, Val: val, Err: err, RTT: rtt}
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
	return rpcErr.ErrorCode() != -32700 && rpcErr.ErrorCode() != -32600
}

// collectOne performs a single JSON-RPC call on one source, dialing lazily if
// needed. It returns the raw result value (or an error for transport/timeout).
func collectOne(ctx context.Context, src *Source, method string, params []any) (json.RawMessage, error) {
	client, err := src.clientForCall(ctx)
	if err != nil {
		return nil, err
	}
	if err := src.verifyChainID(ctx, client); err != nil {
		if errors.Is(err, ErrChainIDMismatch) {
			src.recordFailedCall(err)
		}
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

package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/config"
	upstream "github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
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
	// Evidence is populated by an admission stage after collection. It stays
	// attached to this source and is never combined across sources.
	Evidence json.RawMessage
}

// CollectOptions configures one fan-out. Zero value is a sensible default.
type CollectOptions struct {
	// Sources fixes the exact source objects captured by an outer invocation.
	// Closed members remain visible as failed emissions instead of being
	// replaced by a later membership configuration.
	Sources []*Source
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

// The default fan-out bound lives in config (DefaultCollectTimeout): one
// source for both the YAML and Go construction paths, so they cannot drift
// (decision 0004). A zero CollectOptions.Timeout selects it.

// ErrSourceGated reports that a voting member's request was skipped by a
// scheduling gate (cooldown circuit or freshness bound). The member still
// occupies the invocation's voting basis (decision 0027): this error marks a
// paused request, not a health observation — gated members receive no
// failure accounting.
var ErrSourceGated = errors.New("pool: source gated; request paused, voting basis retained")

// Collect fans a single request out to all eligible sources symmetrically and
// returns each source's raw response, unjudged.
//
// Concurrency model: eligible sources are scheduled independently under one
// shared context. A single source's failure does not affect the others. Sources
// whose calls actually start report health automatically; expiration while
// waiting for a local concurrency slot does not count against a source.
//
// Membership selection: an explicit opts.Sources list is requested
// best-effort, without scheduling gates — no cooldown, freshness, or
// pre-dispatch identity filtering (a known-invalid identity still fails fast
// inside its own call). Gate-paused-but-in-basis semantics belong to
// CollectVoting; Collect serves selection reads where every answer is useful
// evidence and no voting denominator exists.
//
// The returned slice has one Emission per eligible source, in pool order.
func (p *Pool) Collect(ctx context.Context, method string, params []any, opts CollectOptions) ([]Emission, error) {
	return p.collect(ctx, method, params, opts, collectPlan{applyFreshness: true})
}

// CollectVoting fans a single request out to the full voting membership and
// returns one emission per member, in pool order. Membership is captured at
// entry; a concurrent Join or Remove changes only subsequent invocations.
//
// Unlike Collect, scheduling gates pause requests rather than removing
// members (decision 0027): a member whose cooldown circuit is open, whose tip
// trails the freshness bound, or whose identity is known-invalid is not
// requested, but keeps its place in the returned basis — corroboration is
// adjudicated against every voting member, so failures and skips can never
// shrink the denominator. Gated members carry ErrSourceGated (or
// ErrChainIDMismatch) with Attempted=false and receive no health penalty.
//
// A member that fails its lazily-verified chain identity during this
// collection fails as one member; the invocation continues without it.
func (p *Pool) CollectVoting(ctx context.Context, method string, params []any, opts CollectOptions) ([]Emission, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = config.DefaultCollectTimeout()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if p.isClosed() {
		return nil, ErrClosed
	}
	var members []*Source
	if opts.Sources != nil {
		members = append([]*Source(nil), opts.Sources...)
	} else if opts.SourceIDs != nil {
		return nil, fmt.Errorf("pool: CollectVoting fixes its own membership; SourceIDs does not apply")
	} else {
		var err error
		members, err = p.snapshotSources(true)
		if err != nil {
			return nil, err
		}
	}
	if len(members) == 0 {
		return nil, ErrNoEligibleSource
	}

	// Gates are decided before dispatch so paused members never consume a
	// concurrency slot. The freshness reference is computed over the whole
	// basis, as in the eligible path.
	now := time.Now()
	floor := p.staleFloor(members)
	gated := make(map[int]error, len(members))
	attemptable := 0
	for i, src := range members {
		if gate := votingGate(src, floor, now); gate != nil {
			gated[i] = gate
			continue
		}
		attemptable++
	}
	if attemptable == 0 {
		// Preserve the captured basis even when no request can start.
		emissions := make([]Emission, len(members))
		for i, src := range members {
			emissions[i] = Emission{Src: src, Err: gated[i]}
		}
		return emissions, ErrNoEligibleSource
	}

	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 || maxConcurrent > len(members) {
		maxConcurrent = len(members)
	}
	sem := make(chan struct{}, maxConcurrent)

	var (
		emissions = make([]Emission, len(members))
		wg        sync.WaitGroup
	)
	for i, src := range members {
		if gate, skip := gated[i]; skip {
			emissions[i] = Emission{Src: src, Err: gate}
			continue
		}
		dispatchOne(callCtx, ctx, sem, i, src, method, params, emissions, &wg)
	}
	wg.Wait()
	// Usable answers ride alongside a fan-out timeout: the caller adjudicates
	// over the emissions and records the timeout as a fault rather than
	// discarding answered sources.
	if err := callCtx.Err(); err != nil {
		return emissions, err
	}
	for _, emission := range emissions {
		if emission.Val != nil || isJSONRPCResponseError(emission.Err) {
			return emissions, nil
		}
	}
	// Nothing usable: a known-off-chain member names the identity sentinel;
	// otherwise the fleet was contacted but stayed silent.
	for _, emission := range emissions {
		if errors.Is(emission.Err, ErrChainIDMismatch) {
			return emissions, emission.Err
		}
	}
	return emissions, ErrAllSourcesDown
}

// votingGate reports why a member's request is currently paused, or nil when
// it should be dispatched. Closed members are not gated: attempting them
// fails fast with ErrClosed and no penalty, keeping a member removed
// mid-invocation visible in its captured basis.
func votingGate(src *Source, floor uint64, now time.Time) error {
	if src.isIdentityInvalid() {
		return ErrChainIDMismatch
	}
	if !src.requestable(now) {
		return ErrSourceGated
	}
	if floor > 0 {
		if tip, ok := src.tipHeight(); ok && tip < floor {
			return ErrSourceGated
		}
	}
	return nil
}

// dispatchOne attempts one member's call under the shared semaphore and
// records its emission. Attempted calls report health here, so callers cannot
// accidentally hide a dead source by forgetting a separate report step; a
// caller that never acquires a local slot records nothing. Shared by the
// read paths and Broadcast: a broadcast failure cools the source for reads
// too, and a read failure cools it for writes.
func dispatchOne(callCtx, parent context.Context, sem chan struct{}, i int, src *Source, method string, params []any, emissions []Emission, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		var val json.RawMessage
		var err error
		var rtt time.Duration
		attempted := false
		select {
		case sem <- struct{}{}:
			if err = callCtx.Err(); err == nil {
				var release func()
				release, err = src.acquireRequest(callCtx)
				if err != nil && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
					err = fmt.Errorf("%w: %w", ErrSourceGated, err)
				}
				if err == nil {
					attempted = true
					// Released by defer: a panic below must not leak the
					// scheduling slot and slowly wedge this source.
					defer release()
					val, rtt, err = collectOne(callCtx, src, method, params)
				}
			}
			<-sem
		case <-callCtx.Done():
			err = callCtx.Err()
		}

		if attempted {
			if isHealthyRPCResponse(err) {
				src.recordSuccessfulCall(rtt)
			} else if shouldRecordFailure(parent, err) {
				src.recordFailedCall(err)
			}
		}

		emissions[i] = Emission{Src: src, Val: val, Err: err, RTT: rtt, Attempted: attempted}
	}()
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
		timeout = config.DefaultCollectTimeout()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if p.isClosed() {
		return nil, ErrClosed
	}

	var sources []*Source
	var err error
	if opts.Sources != nil {
		sources = append([]*Source(nil), opts.Sources...)
	} else if opts.SourceIDs != nil {
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
		dispatchOne(callCtx, ctx, sem, i, src, method, params, emissions, &wg)
	}
	wg.Wait()

	// Adjudication is a two-pass scan so the verdict cannot depend on member
	// order, with the same priority as Broadcast: a usable answer outranks the
	// fan-out deadline, which outranks the identity sentinel, which outranks
	// fleet silence. CollectVoting keeps its own callCtx-first contract: its
	// caller adjudicates usable emissions alongside the timeout fault.
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
		timeout = config.DefaultCollectTimeout()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if p.isClosed() {
		return nil, ErrClosed
	}

	var sources []*Source
	var err error
	if opts.Sources != nil {
		sources = append([]*Source(nil), opts.Sources...)
	} else if opts.SourceIDs != nil {
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
		release, waitErr := src.acquireRequest(callCtx)
		if waitErr != nil {
			if ctx.Err() == nil && errors.Is(waitErr, context.DeadlineExceeded) {
				waitErr = fmt.Errorf("%w: %w", ErrSourceGated, waitErr)
			}
			emissions = append(emissions, Emission{Src: src, Err: waitErr})
			// A local scheduling gate is not an upstream transport failure;
			// Forward must not turn it into an implicit source failover.
			if errors.Is(waitErr, ErrSourceGated) {
				return emissions, waitErr
			}
			continue
		}
		val, rtt, callErr := collectWithSlot(callCtx, src, method, params, release)
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

// CallSource performs one dependent request against the captured source. It
// is used by admission policies for same-source content (such as uncle
// headers) and deliberately does not alter the voting membership.
func (p *Pool) CallSource(ctx context.Context, src *Source, method string, params []any) (json.RawMessage, error) {
	if src == nil {
		return nil, ErrNoEligibleSource
	}
	// Admission dependencies are part of validating one primary response;
	// malformed or unavailable optional content must not alter source health,
	// but they still consume the source's shared scheduling budget.
	release, err := src.acquireRequest(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	val, _, err := collectOne(ctx, src, method, params)
	return val, err
}

func shouldRecordFailure(parent context.Context, err error) bool {
	return err != nil &&
		parent.Err() == nil &&
		!errors.Is(err, ErrClosed) &&
		!errors.Is(err, errWaitingForDial)
}

func isJSONRPCResponseError(err error) bool {
	var rpcErr rpc.ProtocolError
	return errors.As(err, &rpcErr)
}

func isHealthyRPCResponse(err error) bool {
	if err == nil {
		return true
	}
	var rpcErr rpc.ProtocolError
	if !errors.As(err, &rpcErr) {
		return false
	}
	// These codes mean the peer could not process a valid JSON-RPC exchange.
	return !isProtocolServerError(err) && rpcErr.ErrorCode() != -32700 && rpcErr.ErrorCode() != -32600
}

// collectWithSlot runs collectOne with the source's pacing slot held,
// releasing it even when collectOne panics — parity with dispatchOne's
// defer release, so a panicking adapter cannot permanently wedge the
// source's scheduling budget. The caller records health after the call so
// recording still happens with the slot already freed.
func collectWithSlot(ctx context.Context, src *Source, method string, params []any, release func()) (json.RawMessage, time.Duration, error) {
	defer release()
	return collectOne(ctx, src, method, params)
}

// collectOne performs a single JSON-RPC call on one source, dialing lazily if
// needed. It returns the raw result value (or an error for transport/timeout)
// and the round-trip time of the CallContext exchange alone: lazy dialing and
// the first-use chain-ID verification stay outside the measured window, so
// health percentiles describe the wire rather than connection setup.
func collectOne(ctx context.Context, src *Source, method string, params []any) (json.RawMessage, time.Duration, error) {
	client, err := src.clientForCall(ctx)
	if err != nil {
		return nil, 0, err
	}
	if err := src.verifyChainID(ctx, client); err != nil {
		return nil, 0, err
	}

	var result json.RawMessage
	start := time.Now()
	err = client.CallContext(ctx, &result, method, params...)
	rtt := time.Since(start)
	if src.isClosed() {
		return nil, rtt, ErrClosed
	}
	if err != nil {
		if shouldDiscardClient(err) {
			src.discardClient(client)
		}
		return nil, rtt, err
	}
	return result, rtt, nil
}

func shouldDiscardClient(err error) bool {
	return upstream.IsClientQuit(err)
}

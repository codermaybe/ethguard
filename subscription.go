package ethguard

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	internalsubscription "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// ethSubscription projects a transport subscription into the official
// ethereum.Subscription lifecycle while a typed forwarding goroutine owns
// decoding and channel delivery.
type ethSubscription struct {
	inner  transport.Subscription
	stop   chan struct{}
	done   chan struct{}
	errs   chan error
	cancel context.CancelFunc
	once   sync.Once
}

func (s *ethSubscription) Err() <-chan error { return s.errs }

func (s *ethSubscription) Unsubscribe() {
	s.once.Do(func() {
		close(s.stop)
		if s.cancel != nil {
			s.cancel()
		}
		s.inner.Unsubscribe()
	})
	<-s.done
}

type subscriptionSource interface {
	Subscribe(context.Context, string, ...any) (transport.Subscription, error)
}

func startSubscription[T any](ctx context.Context, inner transport.Subscription, out chan<- T, decode func([]byte) (T, error)) ethereum.Subscription {
	return startSubscriptionWithOwner(ctx, inner, out, decode)
}

func startSubscriptionWithOwner[T any](ownerCtx context.Context, inner transport.Subscription, out chan<- T, decode func([]byte) (T, error)) ethereum.Subscription {
	s := &ethSubscription{inner: inner, stop: make(chan struct{}), done: make(chan struct{}), errs: make(chan error, 1)}
	go func() {
		select {
		case <-ownerCtx.Done():
			s.once.Do(func() { close(s.stop); inner.Unsubscribe() })
		case <-s.done:
		}
	}()
	go func() {
		defer close(s.done)
		defer close(s.errs)
		events, errs := inner.Events(), inner.Err()
		for events != nil || errs != nil {
			select {
			case <-s.stop:
				return
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					s.errs <- err
				}
				return
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				value, err := decode(event.Result)
				if err != nil {
					inner.Unsubscribe()
					s.errs <- fmt.Errorf("ethguard: decode subscription event: %w", err)
					return
				}
				select {
				case out <- value:
				case <-s.stop:
					return
				}
			}
		}
	}()
	return s
}

// subscriptionSources captures the fixed WSS membership used by one
// subscription. The returned identities are stable for the pool lifetime;
// source health never changes this denominator.
func (c *Client) subscriptionSources() ([]identifiedSubscriptionSource, error) {
	sources, err := c.pool.SubscriptionSources()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	out := make([]identifiedSubscriptionSource, len(sources))
	for i, source := range sources {
		out[i] = source
	}
	// A single WSS source inside a multi-source client would silently bypass
	// the client's strict corroboration mode because HTTP members cannot emit
	// subscriptions. Keep that setup fail-closed until an explicit
	// SingleSourceSelected subscription option exists.
	if len(out) == 1 && c.Mode() != rpc.CorroborationForwarded {
		return nil, fmt.Errorf("%w: one WSS source cannot satisfy strict subscription corroboration; configure at least two WSS sources", rpc.ErrInvalidRequest)
	}
	return out, nil
}

type identifiedSubscriptionSource interface {
	subscriptionSource
	ID() string
}

type subscriptionUnarySource interface {
	CallContext(context.Context, string, ...any) (json.RawMessage, error)
}

type subscriptionPullFactory[T any] func(context.Context, identifiedSubscriptionSource, internalsubscription.Kind, string, internalsubscription.Observation) (internalsubscription.Observation, error)

type aggregateEthSubscription[T any] struct {
	inner       map[string]transport.Subscription
	innerMu     sync.Mutex
	agg         *internalsubscription.Aggregator
	stop        chan struct{}
	done        chan struct{}
	errs        chan error
	cancel      context.CancelFunc
	once        sync.Once
	errMu       sync.Mutex
	err         error
	out         chan<- T
	decode      func([]byte, string) (internalsubscription.Observation, T, error)
	faults      chan error
	doneSources chan struct{}
	sessions    *internalsubscription.SessionGate
	pullFactory subscriptionPullFactory[T]
	pullMu      sync.Mutex
	pending     map[string]*pullContext
	probes      map[string]*internalsubscription.PullProbe
	ctx         context.Context
	headGaps    *internalsubscription.HeadGapTracker
	gapMu       sync.Mutex
	gapProbes   map[string]struct{}
	gapWG       sync.WaitGroup
}

type pullContext struct {
	firstSeen   time.Time
	kind        internalsubscription.Kind
	seen        map[string]struct{}
	observation internalsubscription.Observation
}

// subscriptionFault preserves the attribution needed to diagnose an
// asynchronous subscription failure without exposing internal vote state.
type subscriptionFault struct {
	sourceID   string
	generation uint64
	stage      string
	contextKey string
	err        error
}

// ErrSubscriptionGap identifies a new-head height range that could not be
// verified by the same source after the push stream skipped it.
var ErrSubscriptionGap = internalsubscription.ErrHeadGap

// SubscriptionGapError is the terminal error delivered by a multi-source
// new-head subscription when a source gap remains unresolved after the
// bounded same-source Pull window.
type SubscriptionGapError struct {
	SourceID   string
	From       uint64
	To         uint64
	ContextKey string
	Generation uint64
	Cause      error
}

func (e *SubscriptionGapError) Error() string {
	return fmt.Sprintf("ethguard: new-head gap source=%q from=%d to=%d generation=%d context=%q: %v", e.SourceID, e.From, e.To, e.Generation, e.ContextKey, e.Cause)
}

func (e *SubscriptionGapError) Unwrap() error { return e.Cause }

func newSubscriptionGapError(sourceID string, gap internalsubscription.HeadGap, contextKey string, generation uint64, cause error) error {
	if cause == nil {
		cause = ErrSubscriptionGap
	} else {
		cause = errors.Join(ErrSubscriptionGap, cause)
	}
	return &SubscriptionGapError{SourceID: sourceID, From: gap.From, To: gap.To, ContextKey: contextKey, Generation: generation, Cause: cause}
}

func (f *subscriptionFault) Error() string {
	return fmt.Sprintf("ethguard: subscription source=%q generation=%d stage=%s context=%q: %v", f.sourceID, f.generation, f.stage, f.contextKey, f.err)
}

func (f *subscriptionFault) Unwrap() error { return f.err }

func makeSubscriptionFault(sourceID string, generation uint64, stage, contextKey string, err error) error {
	if err == nil {
		return nil
	}
	return &subscriptionFault{sourceID: sourceID, generation: generation, stage: stage, contextKey: contextKey, err: err}
}

func waitSubscriptionRetry(stop <-chan struct{}, ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// maxSubscriptionRetryBackoff caps the reconnect loop's exponential backoff.
// The cap keeps a recovering source responsive (2s, not minutes); the floor
// stays at the loop's 50ms first retry.
const maxSubscriptionRetryBackoff = 2 * time.Second

const (
	subscriptionVerificationWindow = 2 * time.Second
	subscriptionSinglePullTimeout  = 800 * time.Millisecond
	subscriptionPullRetryDelay     = 100 * time.Millisecond
)

func retryableSubscriptionPullError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, internalsubscription.ErrSessionStale) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var status rpc.HTTPStatusError
	if errors.As(err, &status) {
		switch status.HTTPStatusCode() {
		case 429, 502, 503, 504:
			return true
		}
	}
	// Retain the adopted timeout/temporary retry contract for upstream
	// network errors without depending on net.Error's deprecated Temporary
	// member. Both methods are required, so an unrelated Temporary method
	// alone cannot opt an error into retries.
	var networkError interface {
		error
		Timeout() bool
		Temporary() bool
	}
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func remainingPullTimeout(ctx context.Context) time.Duration {
	timeout := subscriptionSinglePullTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	return timeout
}

func waitSubscriptionPullRetry(ctx context.Context, stop <-chan struct{}) bool {
	timer := time.NewTimer(subscriptionPullRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}

func (s *aggregateEthSubscription[T]) Err() <-chan error { return s.errs }

func (s *aggregateEthSubscription[T]) Unsubscribe() {
	s.finish(nil)
	<-s.done
}

func (s *aggregateEthSubscription[T]) finish(err error) {
	if err != nil {
		s.errMu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.errMu.Unlock()
	}
	s.once.Do(func() {
		close(s.stop)
		if s.cancel != nil {
			s.cancel()
		}
		s.innerMu.Lock()
		inners := make([]transport.Subscription, 0, len(s.inner))
		for _, inner := range s.inner {
			inners = append(inners, inner)
		}
		s.innerMu.Unlock()
		for _, inner := range inners {
			go inner.Unsubscribe()
		}
		s.pullMu.Lock()
		probes := make([]*internalsubscription.PullProbe, 0, len(s.probes))
		for _, probe := range s.probes {
			probes = append(probes, probe)
		}
		s.pullMu.Unlock()
		for _, probe := range probes {
			go probe.Stop()
		}
		s.agg.Close()
		if s.sessions != nil {
			s.sessions.Close()
		}
	})
}

func (s *aggregateEthSubscription[T]) terminalError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *aggregateEthSubscription[T]) noteObservation(obs internalsubscription.Observation) {
	if s.pullFactory == nil || obs.ContextKey == "" {
		return
	}
	s.pullMu.Lock()
	defer s.pullMu.Unlock()
	if !obs.Removed {
		status := s.agg.Status(obs.ContextKey)
		if status == internalsubscription.Decided || status == internalsubscription.Selected || status == internalsubscription.Delivered || status == internalsubscription.Revoked {
			return
		}
	}
	state := s.pending[obs.ContextKey]
	if state == nil {
		state = &pullContext{firstSeen: obs.ObservedAt, kind: obs.Kind, seen: make(map[string]struct{})}
		if state.firstSeen.IsZero() {
			state.firstSeen = time.Now()
		}
		s.pending[obs.ContextKey] = state
	}
	if len(state.observation.Payload) == 0 && len(obs.Payload) > 0 {
		state.observation = obs
	}
	state.seen[obs.SourceID] = struct{}{}
}

func (s *aggregateEthSubscription[T]) startPendingPulls(now time.Time, sources []identifiedSubscriptionSource) {
	if s.pullFactory == nil {
		return
	}
	s.pullMu.Lock()
	defer s.pullMu.Unlock()
	for contextKey, state := range s.pending {
		status := s.agg.Status(contextKey)
		if status == internalsubscription.Decided || status == internalsubscription.Selected || status == internalsubscription.Delivered || status == internalsubscription.Revoked {
			if state.kind != internalsubscription.KindLogs || !state.observation.Removed {
				delete(s.pending, contextKey)
				if probe := s.probes[contextKey]; probe != nil {
					go probe.Stop()
					delete(s.probes, contextKey)
				}
				continue
			}
		}
		if now.Sub(state.firstSeen) < 100*time.Millisecond {
			continue
		}
		if _, running := s.probes[contextKey]; running {
			continue
		}
		requests := make([]internalsubscription.PullRequest, 0, len(sources))
		aggregateGeneration := s.agg.Snapshot().Generation
		for _, source := range sources {
			if _, ok := state.seen[source.ID()]; ok {
				continue
			}
			session, sessionErr := s.sessions.Current(source.ID())
			if sessionErr != nil {
				continue
			}
			requestGeneration := session.Generation
			requests = append(requests, internalsubscription.PullRequest{
				Kind: state.kind, ContextKey: contextKey, SourceID: source.ID(), Generation: requestGeneration, AggregateGeneration: aggregateGeneration, FirstSeen: state.firstSeen,
				Pull: func(pctx context.Context) (internalsubscription.Observation, error) {
					for {
						currentSession, currentErr := s.sessions.Current(source.ID())
						if currentErr != nil {
							if retryableSubscriptionPullError(currentErr) && waitSubscriptionPullRetry(pctx, s.stop) {
								continue
							}
							return internalsubscription.Observation{}, currentErr
						}
						timeout := remainingPullTimeout(pctx)
						if timeout <= 0 {
							return internalsubscription.Observation{}, context.DeadlineExceeded
						}
						attemptCtx, cancel := context.WithTimeout(pctx, timeout)
						seed := state.observation
						seed.Generation = currentSession.Generation
						observation, pullErr := s.pullFactory(attemptCtx, source, state.kind, contextKey, seed)
						cancel()
						if pullErr == nil {
							pullErr = s.sessions.Accept(currentSession)
						}
						if pullErr == nil {
							// PullProbe owns the fixed request attribution. The
							// current session was validated immediately before
							// and after the same-source Pull above.
							observation.Generation = requestGeneration
							return observation, nil
						}
						if !retryableSubscriptionPullError(pullErr) || !waitSubscriptionPullRetry(pctx, s.stop) {
							return internalsubscription.Observation{}, pullErr
						}
					}
				},
			})
		}
		if len(requests) == 0 {
			continue
		}
		probe, err := internalsubscription.StartPullProbe(s.ctx, s.agg, requests, internalsubscription.PullProbeConfig{SoftTimeout: 0, HardTimeout: subscriptionVerificationWindow})
		if err != nil {
			continue
		}
		s.probes[contextKey] = probe
		go func(key string, p *internalsubscription.PullProbe) {
			for err := range p.Err() {
				select {
				case s.faults <- err:
				case <-s.stop:
					return
				}
			}
			s.pullMu.Lock()
			delete(s.probes, key)
			s.pullMu.Unlock()
		}(contextKey, probe)
	}
}

func (s *aggregateEthSubscription[T]) observeHeadGap(source identifiedSubscriptionSource, session internalsubscription.SourceSession, observation internalsubscription.Observation) {
	if observation.Kind != internalsubscription.KindNewHeads || !strings.HasPrefix(observation.ContextKey, "head:") || s.headGaps == nil {
		return
	}
	height, err := strconv.ParseUint(strings.TrimPrefix(observation.ContextKey, "head:"), 10, 64)
	if err != nil {
		return
	}
	gap := s.headGaps.Observe(source.ID(), height)
	if gap == nil {
		return
	}
	key := fmt.Sprintf("%s/%d/%d/%d", source.ID(), session.Generation, gap.From, gap.To)
	s.gapMu.Lock()
	if _, running := s.gapProbes[key]; running {
		s.gapMu.Unlock()
		return
	}
	s.gapProbes[key] = struct{}{}
	s.gapWG.Add(1)
	s.gapMu.Unlock()
	go s.runHeadGapProbe(key, source, session, *gap, observation.ContextKey)
}

func (s *aggregateEthSubscription[T]) runHeadGapProbe(key string, source identifiedSubscriptionSource, session internalsubscription.SourceSession, gap internalsubscription.HeadGap, contextKey string) {
	defer s.gapWG.Done()
	defer func() {
		s.gapMu.Lock()
		delete(s.gapProbes, key)
		s.gapMu.Unlock()
	}()
	soft := time.NewTimer(subscriptionPullRetryDelay)
	defer soft.Stop()
	select {
	case <-s.stop:
		return
	case <-s.ctx.Done():
		return
	case <-soft.C:
	}
	unary, ok := source.(subscriptionUnarySource)
	if !ok {
		if !s.isStopping() {
			s.finish(newSubscriptionGapError(source.ID(), gap, contextKey, session.Generation, fmt.Errorf("source has no same-source unary Pull")))
		}
		return
	}
	pullCtx, cancel := context.WithTimeout(s.ctx, subscriptionVerificationWindow)
	defer cancel()
	var probeErr error
	var lastGeneration = session.Generation
	for height := gap.From; ; height++ {
		for {
			currentSession, currentErr := s.sessions.Current(source.ID())
			if currentErr != nil {
				probeErr = currentErr
			} else {
				lastGeneration = currentSession.Generation
				timeout := remainingPullTimeout(pullCtx)
				if timeout <= 0 {
					probeErr = context.DeadlineExceeded
				} else {
					attemptCtx, attemptCancel := context.WithTimeout(pullCtx, timeout)
					raw, callErr := unary.CallContext(attemptCtx, "eth_getBlockByNumber", fmt.Sprintf("0x%x", height), false)
					attemptCancel()
					if callErr != nil {
						probeErr = callErr
					} else {
						header, decodeErr := typed.DecodeHeader(raw)
						if decodeErr != nil {
							probeErr = decodeErr
						} else if header.Number == nil || header.Number.BitLen() > 64 || header.Number.Uint64() != height {
							probeErr = fmt.Errorf("pulled header number does not match height %d", height)
						} else {
							probeErr = s.sessions.Accept(currentSession)
						}
					}
				}
			}
			if probeErr == nil {
				break
			}
			if !retryableSubscriptionPullError(probeErr) || !waitSubscriptionPullRetry(pullCtx, s.stop) {
				if s.isStopping() || errors.Is(probeErr, context.Canceled) && s.ctx.Err() != nil {
					return
				}
				s.finish(newSubscriptionGapError(source.ID(), gap, contextKey, lastGeneration, probeErr))
				return
			}
		}
		if height == gap.To {
			return
		}
	}
}

func (s *aggregateEthSubscription[T]) isStopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func startAggregatedSubscription[T any](ctx context.Context, sources []identifiedSubscriptionSource, kind internalsubscription.Kind, args []any, out chan<- T, decode func([]byte, string) (internalsubscription.Observation, T, error)) (ethereum.Subscription, error) {
	return startAggregatedSubscriptionWithPull(ctx, sources, kind, args, out, decode, nil)
}

func startAggregatedSubscriptionWithPull[T any](ctx context.Context, sources []identifiedSubscriptionSource, kind internalsubscription.Kind, args []any, out chan<- T, decode func([]byte, string) (internalsubscription.Observation, T, error), pullFactory subscriptionPullFactory[T]) (ethereum.Subscription, error) {
	return startAggregatedSubscriptionWithPullOwner(ctx, ctx, sources, kind, args, out, decode, pullFactory)
}

func startAggregatedSubscriptionWithPullOwner[T any](ownerCtx, setupCtx context.Context, sources []identifiedSubscriptionSource, kind internalsubscription.Kind, args []any, out chan<- T, decode func([]byte, string) (internalsubscription.Observation, T, error), pullFactory subscriptionPullFactory[T]) (ethereum.Subscription, error) {
	if len(sources) < 2 {
		return nil, fmt.Errorf("ethguard: aggregated subscription requires at least two sources")
	}
	members := make([]string, len(sources))
	for i, source := range sources {
		members[i] = source.ID()
	}
	quorum := len(members)/2 + 1
	if quorum < 2 {
		quorum = 2
	}
	generations := make(map[string]uint64, len(members))
	for _, member := range members {
		generations[member] = 1
	}
	state, err := internalsubscription.New(internalsubscription.Config{
		Snapshot: internalsubscription.Snapshot{ID: fmt.Sprintf("subscription-%d", time.Now().UnixNano()), Generation: 1, Generations: generations, Members: members, Quorum: quorum},
		Mode:     internalsubscription.Strict, Queue: 256, HardTimeout: 30 * time.Second,
		Budget: internalsubscription.Budget{MaxPayloadBytes: 8 << 20, MaxEvidenceBytes: 64 << 20},
	})
	if err != nil {
		return nil, err
	}
	merged, cancel := context.WithCancel(context.WithoutCancel(ownerCtx))
	sessions, sessionErr := internalsubscription.NewSessionGate(members)
	if sessionErr != nil {
		cancel()
		return nil, sessionErr
	}
	s := &aggregateEthSubscription[T]{inner: make(map[string]transport.Subscription, len(sources)), agg: state, stop: make(chan struct{}), done: make(chan struct{}), errs: make(chan error, 1), cancel: cancel, out: out, decode: decode, faults: make(chan error, len(sources)), doneSources: make(chan struct{}), sessions: sessions, pullFactory: pullFactory, pending: make(map[string]*pullContext), probes: make(map[string]*internalsubscription.PullProbe), ctx: merged, headGaps: internalsubscription.NewHeadGapTracker(), gapProbes: make(map[string]struct{})}
	go func() {
		select {
		case <-ownerCtx.Done():
			s.finish(context.Cause(ownerCtx))
		case <-s.stop:
		case <-s.done:
		}
	}()
	initialSessions := make([]internalsubscription.SourceSession, len(sources))
	initialSubscriptions := make([]transport.Subscription, len(sources))
	for i, source := range sources {
		if err := setupCtx.Err(); err != nil {
			s.finish(makeSubscriptionFault("", 1, "lifecycle", "", err))
			return nil, err
		}
		session, openErr := sessions.Open(source.ID())
		if openErr != nil {
			s.finish(openErr)
			return nil, openErr
		}
		inner, subErr := source.Subscribe(setupCtx, string(kind), args...)
		if subErr != nil {
			s.finish(makeSubscriptionFault(source.ID(), 1, "subscribe", "", subErr))
			return nil, subErr
		}
		if err := setupCtx.Err(); err != nil {
			go inner.Unsubscribe()
			s.finish(makeSubscriptionFault("", 1, "lifecycle", "", err))
			return nil, err
		}
		initialSessions[i] = session
		initialSubscriptions[i] = inner
		// The reconnect worker rechecks s.stop under the inner lock for the
		// same reason: finish may have snapshotted and torn down s.inner
		// while this Subscribe was in flight, and an inner published after
		// that snapshot has no one left to Unsubscribe it. The initial
		// establish loop owns the same invariant on its own write path.
		s.innerMu.Lock()
		select {
		case <-s.stop:
			s.innerMu.Unlock()
			go inner.Unsubscribe()
			cause := context.Cause(ownerCtx)
			if cause == nil {
				cause = ErrPoolClosed
			}
			s.finish(makeSubscriptionFault("", 1, "lifecycle", "", cause))
			return nil, cause
		default:
			s.inner[source.ID()] = inner
			s.innerMu.Unlock()
		}
	}
	var sourceWG sync.WaitGroup
	sourceWG.Add(len(sources))
	for i, source := range sources {
		sourceID := members[i]
		go func() {
			defer sourceWG.Done()
			inner := initialSubscriptions[i]
			session := initialSessions[i]
			for {
				events, errs := inner.Events(), inner.Err()
				reconnect := false
				for events != nil || errs != nil {
					select {
					case <-s.stop:
						return
					case err, ok := <-errs:
						if ok && err != nil {
							select {
							case s.faults <- makeSubscriptionFault(sourceID, 1, "transport", "", err):
							default:
							}
						}
						reconnect = true
						events, errs = nil, nil
					case event, ok := <-events:
						if !ok {
							events = nil
							continue
						}
						// The current WSS adapter leaves source identity and
						// generation unset for its initial stream. When an adapter
						// supplies them, reject frames that belong to another source
						// or an older physical session before they enter quorum.
						if event.SourceID != "" && event.SourceID != sourceID {
							continue
						}
						candidateSession := session
						if event.Generation != 0 {
							candidateSession.Generation = event.Generation
						}
						if errors.Is(s.sessions.Accept(candidateSession), internalsubscription.ErrSessionStale) {
							continue
						}
						observation, _, decodeErr := s.decode(event.Result, sourceID)
						if decodeErr != nil {
							s.finish(makeSubscriptionFault(sourceID, 1, "decode", "", decodeErr))
							return
						}
						// The physical session generation is enforced by SessionGate;
						// the fixed quorum snapshot keeps its logical generation stable.
						observation.Generation = 1
						s.observeHeadGap(source, candidateSession, observation)
						if submitErr := s.agg.Submit(observation); submitErr != nil && !errors.Is(submitErr, internalsubscription.ErrStaleGeneration) && !errors.Is(submitErr, internalsubscription.ErrUnknownSource) {
							s.finish(makeSubscriptionFault(sourceID, observation.Generation, "aggregate", observation.ContextKey, submitErr))
							return
						}
						s.noteObservation(observation)
					}
					if reconnect {
						break
					}
				}
				if !reconnect {
					// Both transport channels closed without an explicit error.
					reconnect = true
				}
				if !reconnect || !waitSubscriptionRetry(s.stop, merged, 50*time.Millisecond) {
					return
				}
				retryDelay := 50 * time.Millisecond
				for {
					nextSession, openErr := sessions.Open(sourceID)
					if openErr != nil {
						return
					}
					nextInner, subErr := source.Subscribe(merged, string(kind), args...)
					if subErr == nil {
						s.innerMu.Lock()
						select {
						case <-s.stop:
							s.innerMu.Unlock()
							nextInner.Unsubscribe()
							return
						default:
							session, inner = nextSession, nextInner
							s.inner[sourceID] = inner
							s.innerMu.Unlock()
						}
						break
					}
					select {
					case s.faults <- makeSubscriptionFault(sourceID, nextSession.Generation, "reconnect", "", subErr):
					default:
					}
					// Bounded exponential backoff: a dead endpoint must not
					// be retried at a fixed ~10 Hz forever. The sequence
					// caps at 2s; the stop channel still cuts every wait.
					retryDelay *= 2
					if retryDelay > maxSubscriptionRetryBackoff {
						retryDelay = maxSubscriptionRetryBackoff
					}
					if !waitSubscriptionRetry(s.stop, merged, retryDelay) {
						return
					}
				}
			}
		}()
	}
	go func() {
		sourceWG.Wait()
		close(s.doneSources)
	}()
	go func() {
		defer close(s.done)
		defer s.gapWG.Wait()
		defer func() { <-s.doneSources }()
		defer func() {
			if err := s.terminalError(); err != nil {
				s.errs <- err
			}
			close(s.errs)
		}()
		events, errs := s.agg.Events(), s.agg.Err()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var firstFault error
		for events != nil || errs != nil {
			select {
			case <-s.stop:
				return
			case err := <-s.faults:
				if firstFault == nil {
					firstFault = err
				}
			case <-s.doneSources:
				for {
					select {
					case err := <-s.faults:
						if firstFault == nil {
							firstFault = err
						}
					default:
						goto faultsDrained
					}
				}
			faultsDrained:
				s.finish(firstFault)
				return
			case now := <-ticker.C:
				s.agg.Advance(now)
				s.startPendingPulls(now, sources)
			case err, ok := <-errs:
				if ok && err != nil {
					s.finish(err)
				}
				errs = nil
			case delivery, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				s.pullMu.Lock()
				delete(s.pending, delivery.ContextKey)
				probe := s.probes[delivery.ContextKey]
				delete(s.probes, delivery.ContextKey)
				s.pullMu.Unlock()
				if probe != nil {
					go probe.Stop()
				}
				_, value, decodeErr := s.decode(delivery.Payload, delivery.SourceID)
				if decodeErr != nil {
					s.finish(makeSubscriptionFault(delivery.SourceID, delivery.Generation, "deliver", delivery.ContextKey, decodeErr))
					return
				}
				select {
				case s.out <- value:
				case <-s.stop:
					return
				}
			}
		}
	}()
	return s, nil
}

func (c *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	if len(sources) == 1 {
		inner, subErr := sources[0].Subscribe(ctx, "newHeads")
		if subErr != nil {
			return nil, subErr
		}
		return startSubscriptionWithOwner(c.lifeCtx, inner, ch, func(raw []byte) (*types.Header, error) {
			return typed.DecodeHeader(raw)
		}), nil
	}
	return startAggregatedSubscriptionWithPullOwner(c.lifeCtx, ctx, sources, internalsubscription.KindNewHeads, nil, ch, func(raw []byte, sourceID string) (internalsubscription.Observation, *types.Header, error) {
		header, decodeErr := typed.DecodeHeader(raw)
		if decodeErr != nil {
			return internalsubscription.Observation{}, nil, decodeErr
		}
		if header.Number == nil {
			return internalsubscription.Observation{}, nil, fmt.Errorf("header number is nil")
		}
		contextKey, key, keyErr := internalsubscription.CanonicalNewHeadKey(header.Number.String(), header.Hash().Hex(), header.ParentHash.Hex())
		if keyErr != nil {
			return internalsubscription.Observation{}, nil, keyErr
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, header, nil
	}, func(pctx context.Context, source identifiedSubscriptionSource, kind internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		unary, ok := source.(subscriptionUnarySource)
		if !ok || kind != internalsubscription.KindNewHeads || !strings.HasPrefix(contextKey, "head:") {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: source %q has no same-source head Pull", source.ID())
		}
		number := strings.TrimPrefix(contextKey, "head:")
		height, err := strconv.ParseUint(number, 10, 64)
		if err != nil {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: invalid head context %q: %w", contextKey, err)
		}
		raw, err := unary.CallContext(pctx, "eth_getBlockByNumber", fmt.Sprintf("0x%x", height), false)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		header, err := typed.DecodeHeader(raw)
		if err != nil || header.Number == nil {
			if err == nil {
				err = fmt.Errorf("header number is nil")
			}
			return internalsubscription.Observation{}, err
		}
		gotContext, key, err := internalsubscription.CanonicalNewHeadKey(header.Number.String(), header.Hash().Hex(), header.ParentHash.Hex())
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		if gotContext != contextKey {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: Pull head context %q does not match %q", gotContext, contextKey)
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindNewHeads, ContextKey: gotContext, Key: key, SourceID: source.ID(), Generation: seed.Generation, Payload: append([]byte(nil), raw...)}, nil
	})
}

func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	arg, err := toFilterArg(q)
	if err != nil {
		return nil, err
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	if len(sources) == 1 {
		inner, subErr := sources[0].Subscribe(ctx, "logs", arg)
		if subErr != nil {
			return nil, subErr
		}
		return startSubscriptionWithOwner(c.lifeCtx, inner, ch, func(raw []byte) (types.Log, error) {
			if err := validateSubscriptionLogIdentity(raw); err != nil {
				return types.Log{}, err
			}
			return typed.DecodeLog(raw)
		}), nil
	}
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, types.Log, error) {
		if err := validateSubscriptionLogIdentity(raw); err != nil {
			return internalsubscription.Observation{}, types.Log{}, err
		}
		logValue, decodeErr := typed.DecodeLog(raw)
		if decodeErr != nil {
			return internalsubscription.Observation{}, types.Log{}, decodeErr
		}
		topics := make([]string, len(logValue.Topics))
		for i, topic := range logValue.Topics {
			topics[i] = topic.Hex()
		}
		contextKey, key, keyErr := internalsubscription.CanonicalLogKey(logValue.BlockHash.Hex(), fmt.Sprintf("0x%x", logValue.BlockNumber), logValue.TxHash.Hex(), uint64(logValue.Index), logValue.Address.Hex(), hex.EncodeToString(logValue.Data), topics, logValue.Removed)
		if keyErr != nil {
			return internalsubscription.Observation{}, types.Log{}, keyErr
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindLogs, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...), Removed: logValue.Removed}, logValue, nil
	}
	return startAggregatedSubscriptionWithPullOwner(c.lifeCtx, ctx, sources, internalsubscription.KindLogs, []any{arg}, ch, decode, func(pctx context.Context, source identifiedSubscriptionSource, kind internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		if kind != internalsubscription.KindLogs {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: invalid logs Pull kind %q", kind)
		}
		unary, ok := source.(subscriptionUnarySource)
		if !ok {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: source %q has no same-source logs Pull", source.ID())
		}
		seedLog, err := typed.DecodeLog(seed.Payload)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		pullArg, err := toFilterArg(ethereum.FilterQuery{BlockHash: &seedLog.BlockHash, Addresses: q.Addresses, Topics: q.Topics})
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		raw, err := unary.CallContext(pctx, "eth_getLogs", pullArg)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		logs, err := typed.DecodeLogs(raw)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		for i := range logs {
			candidateRaw, marshalErr := json.Marshal(logs[i])
			if marshalErr != nil {
				return internalsubscription.Observation{}, marshalErr
			}
			observation, _, decodeErr := decode(candidateRaw, source.ID())
			if decodeErr != nil {
				return internalsubscription.Observation{}, decodeErr
			}
			if observation.ContextKey == contextKey {
				observation.Generation = seed.Generation
				return observation, nil
			}
		}
		return internalsubscription.Observation{}, fmt.Errorf("ethguard: logs Pull did not return context %q", contextKey)
	})
}

func validateSubscriptionLogIdentity(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("ethguard: subscription log is not an object")
	}
	for _, field := range []string{"blockHash", "transactionHash", "logIndex"} {
		value, ok := fields[field]
		if !ok || string(value) == "null" || len(value) == 0 {
			return fmt.Errorf("ethguard: subscription log missing %s", field)
		}
	}
	return nil
}

func (c *Client) SubscribeTransactionReceipts(ctx context.Context, q *ethereum.TransactionReceiptsQuery, ch chan<- []*types.Receipt) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	if len(sources) == 1 {
		// Normalize the nil query exactly like the multi-source path below:
		// both routes must put the same wire spelling on the socket, or the
		// single-source route would send a bare null parameter that
		// providers may reject.
		singleQuery := q
		if singleQuery == nil {
			singleQuery = &ethereum.TransactionReceiptsQuery{}
		}
		inner, subErr := sources[0].Subscribe(ctx, "transactionReceipts", singleQuery)
		if subErr != nil {
			return nil, subErr
		}
		return startSubscriptionWithOwner(c.lifeCtx, inner, ch, func(raw []byte) ([]*types.Receipt, error) {
			return typed.DecodeReceipts(raw)
		}), nil
	}
	query := q
	if query == nil {
		query = &ethereum.TransactionReceiptsQuery{}
	}
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, []*types.Receipt, error) {
		receipts, decodeErr := typed.DecodeReceipts(raw)
		if decodeErr != nil {
			return internalsubscription.Observation{}, nil, decodeErr
		}
		identities := make([]internalsubscription.ReceiptIdentity, len(receipts))
		for i, receipt := range receipts {
			if receipt == nil || receipt.BlockNumber == nil {
				return internalsubscription.Observation{}, nil, fmt.Errorf("ethguard: receipt %d has no block identity", i)
			}
			canonical, marshalErr := json.Marshal(receipt)
			if marshalErr != nil {
				return internalsubscription.Observation{}, nil, marshalErr
			}
			identities[i] = internalsubscription.ReceiptIdentity{
				TransactionHash:  receipt.TxHash.Hex(),
				BlockHash:        receipt.BlockHash.Hex(),
				BlockNumber:      receipt.BlockNumber.String(),
				TransactionIndex: uint64(receipt.TransactionIndex),
				Canonical:        canonical,
			}
		}
		contextKey, key, keyErr := internalsubscription.CanonicalTransactionReceiptsKey(identities)
		if keyErr != nil {
			return internalsubscription.Observation{}, nil, keyErr
		}
		return internalsubscription.Observation{Kind: internalsubscription.KindTransactionReceipts, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, receipts, nil
	}
	return startAggregatedSubscriptionWithPullOwner(c.lifeCtx, ctx, sources, internalsubscription.KindTransactionReceipts, []any{query}, ch, decode, func(pctx context.Context, source identifiedSubscriptionSource, kind internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		if kind != internalsubscription.KindTransactionReceipts {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: invalid transactionReceipts Pull kind %q", kind)
		}
		unary, ok := source.(subscriptionUnarySource)
		if !ok {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: source %q has no same-source receipt Pull", source.ID())
		}
		seedReceipts, err := typed.DecodeReceipts(seed.Payload)
		if err != nil {
			return internalsubscription.Observation{}, err
		}
		if len(seedReceipts) == 0 {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: cannot Pull empty receipt batch")
		}
		blockHash := seedReceipts[0].BlockHash
		for i, receipt := range seedReceipts {
			if receipt == nil || receipt.BlockHash != blockHash {
				return internalsubscription.Observation{}, fmt.Errorf("ethguard: receipt %d has inconsistent block identity", i)
			}
		}
		raw, callErr := unary.CallContext(pctx, "eth_getBlockReceipts", blockHash)
		if callErr != nil {
			return internalsubscription.Observation{}, callErr
		}
		allReceipts, decodeErr := typed.DecodeReceipts(raw)
		if decodeErr != nil {
			return internalsubscription.Observation{}, decodeErr
		}
		wanted := make(map[common.Hash]struct{}, len(query.TransactionHashes))
		for _, hash := range query.TransactionHashes {
			wanted[hash] = struct{}{}
		}
		seedHashes := make(map[common.Hash]struct{}, len(seedReceipts))
		for _, receipt := range seedReceipts {
			seedHashes[receipt.TxHash] = struct{}{}
		}
		filtered := make([]*types.Receipt, 0, len(allReceipts))
		for _, receipt := range allReceipts {
			if receipt.BlockHash != blockHash {
				return internalsubscription.Observation{}, fmt.Errorf("ethguard: block receipt has inconsistent block identity")
			}
			if len(wanted) == 0 {
				filtered = append(filtered, receipt)
				continue
			}
			if _, ok := wanted[receipt.TxHash]; ok {
				filtered = append(filtered, receipt)
			}
		}
		for _, receipt := range filtered {
			delete(seedHashes, receipt.TxHash)
		}
		if len(seedHashes) > 0 {
			for hash := range seedHashes {
				return internalsubscription.Observation{}, fmt.Errorf("ethguard: block receipt Pull omitted seeded transaction %s", hash)
			}
		}
		if len(filtered) == 0 {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: block receipt Pull returned no matching receipts")
		}
		batch, marshalErr := json.Marshal(filtered)
		if marshalErr != nil {
			return internalsubscription.Observation{}, marshalErr
		}
		observation, _, decodeErr := decode(batch, source.ID())
		if decodeErr != nil {
			return internalsubscription.Observation{}, decodeErr
		}
		if observation.ContextKey != contextKey {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: receipt Pull context %q does not match %q", observation.ContextKey, contextKey)
		}
		observation.Generation = seed.Generation
		return observation, nil
	})
}

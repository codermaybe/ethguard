package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"syscall"
	"time"

	ethereum "github.com/ethereum/go-ethereum"

	"github.com/codermaybe/ethguard/ethapi"
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
		defer s.once.Do(func() { close(s.stop); inner.Unsubscribe() })
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
				releaseSubscriptionNotification(event)
				if err != nil {
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

func releaseSubscriptionNotification(event transport.Notification) {
	if event.Release != nil {
		event.Release()
	}
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
	cleanupWG   sync.WaitGroup
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
			s.cleanupWG.Go(inner.Unsubscribe)
		}
		s.pullMu.Lock()
		probes := make([]*internalsubscription.PullProbe, 0, len(s.probes))
		for _, probe := range s.probes {
			probes = append(probes, probe)
		}
		s.pullMu.Unlock()
		for _, probe := range probes {
			s.cleanupWG.Go(probe.Stop)
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
					s.cleanupWG.Go(probe.Stop)
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
		s.cleanupWG.Add(1)
		go func(key string, p *internalsubscription.PullProbe) {
			defer s.cleanupWG.Done()
			defer p.Stop()
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
		Mode:     internalsubscription.StrictMode, Queue: 256, HardTimeout: 30 * time.Second,
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
	s := &aggregateEthSubscription[T]{inner: make(map[string]transport.Subscription, len(sources)), agg: state, stop: make(chan struct{}), done: make(chan struct{}), errs: make(chan error, 1), cancel: cancel, out: out, decode: decode, faults: make(chan error, len(sources)), doneSources: make(chan struct{}), sessions: sessions, pullFactory: pullFactory, pending: make(map[string]*pullContext), probes: make(map[string]*internalsubscription.PullProbe), ctx: merged}
	go func() {
		select {
		case <-ownerCtx.Done():
			s.finish(context.Cause(ownerCtx))
		case <-s.stop:
		case <-s.done:
		}
	}()
	established := false
	defer func() {
		if !established {
			s.finish(nil)
			s.cleanupWG.Wait()
		}
	}()
	initialSessions := make([]internalsubscription.SourceSession, len(sources))
	initialSubscriptions := make([]transport.Subscription, len(sources))
	for i, source := range sources {
		if err := setupCtx.Err(); err != nil {
			s.finish(makeSubscriptionFault("", 1, "lifecycle", "", err))
			return nil, err
		}
		inner, subErr := source.Subscribe(setupCtx, string(kind), args...)
		if subErr != nil {
			s.finish(makeSubscriptionFault(source.ID(), 1, "subscribe", "", subErr))
			return nil, subErr
		}
		if err := setupCtx.Err(); err != nil {
			s.cleanupWG.Go(inner.Unsubscribe)
			s.finish(makeSubscriptionFault("", 1, "lifecycle", "", err))
			return nil, err
		}
		session, openErr := sessions.Open(source.ID())
		if openErr != nil {
			s.cleanupWG.Go(inner.Unsubscribe)
			s.finish(openErr)
			return nil, openErr
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
			s.cleanupWG.Go(inner.Unsubscribe)
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
							releaseSubscriptionNotification(event)
							continue
						}
						candidateSession := session
						if event.Generation != 0 {
							candidateSession.Generation = event.Generation
						}
						if s.sessions.Accept(candidateSession) != nil {
							releaseSubscriptionNotification(event)
							continue
						}
						observation, _, decodeErr := s.decode(event.Result, sourceID)
						releaseSubscriptionNotification(event)
						if decodeErr != nil {
							s.finish(makeSubscriptionFault(sourceID, 1, "decode", "", decodeErr))
							return
						}
						// The physical session generation is enforced by SessionGate;
						// the fixed quorum snapshot keeps its logical generation stable.
						observation.Generation = 1
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
				// Retire attribution before cleanup can wait on the network. The
				// source worker owns each old handle until its teardown completes.
				sessions.Retire(session)
				inner.Unsubscribe()
				if !reconnect || !waitSubscriptionRetry(s.stop, merged, 50*time.Millisecond) {
					return
				}
				retryDelay := 50 * time.Millisecond
				for {
					nextInner, subErr := source.Subscribe(merged, string(kind), args...)
					if subErr == nil {
						s.innerMu.Lock()
						select {
						case <-s.stop:
							s.innerMu.Unlock()
							nextInner.Unsubscribe()
							return
						default:
							nextSession, openErr := sessions.Open(sourceID)
							if openErr != nil {
								s.innerMu.Unlock()
								nextInner.Unsubscribe()
								return
							}
							session, inner = nextSession, nextInner
							s.inner[sourceID] = inner
							s.innerMu.Unlock()
						}
						break
					}
					select {
					case s.faults <- makeSubscriptionFault(sourceID, session.Generation, "reconnect", "", subErr):
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
		defer func() {
			// finish joins its once body before we wait, so every teardown
			// task is registered. Source workers finish any late setup cleanup;
			// no producer can add cleanup work after these joins.
			s.finish(nil)
			<-s.doneSources
			s.cleanupWG.Wait()
			// Only this completed handle remains. Drop references to payload
			// history and callbacks after every reader and cleanup task joins.
			s.inner = nil
			s.pending = nil
			s.probes = nil
			s.agg = nil
			s.out = nil
			s.decode = nil
			s.pullFactory = nil
		}()
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
		var output chan<- T
		var value T
		for events != nil || errs != nil || output != nil {
			ready := events
			if output != nil {
				// Keep one FIFO delivery pending while the control loop still
				// advances deadlines and receives terminal errors.
				ready = nil
			}
			select {
			case <-s.stop:
				return
			case output <- value:
				output = nil
				var zero T
				value = zero
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
			case delivery, ok := <-ready:
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
					s.cleanupWG.Go(probe.Stop)
				}
				_, decoded, decodeErr := s.decode(delivery.Payload, delivery.SourceID)
				if decodeErr != nil {
					s.finish(makeSubscriptionFault(delivery.SourceID, delivery.Generation, "deliver", delivery.ContextKey, decodeErr))
					return
				}
				value, output = decoded, s.out
			}
		}
	}()
	established = true
	return s, nil
}

// subscribeSingleSource rides the one-source bridge: subscribe the source's
// push stream and forward typed events under the owner lifecycle. Decode
// failures terminate the subscription with their error.
func subscribeSingleSource[T any](setupCtx, ownerCtx context.Context, source identifiedSubscriptionSource, method string, args []any, out chan<- T, decode func([]byte) (T, error)) (ethereum.Subscription, error) {
	inner, err := source.Subscribe(setupCtx, method, args...)
	if err != nil {
		return nil, err
	}
	return startSubscriptionWithOwner(ownerCtx, inner, out, decode), nil
}

// SubscribeNewHead opens a typed stream of atomic path changes.
func (c *Client) SubscribeNewHead(ctx context.Context, policy HeadSubscriptionPolicy) (*HeadSubscription, error) {
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	return startNewHeadSubscription(c.lifeCtx, ctx, sources, policy, c.Mode())
}

// SubscribeFilterLogs follows the committed Head path from its birth anchor.
// Each Next result is a complete path delta, including zero-match blocks.
// Numeric/hash filters select that live path without replaying old history;
// crossing toBlock does not stop reorg tracking. latest is live, while safe,
// finalized, pending and unknown filter extensions are rejected locally.
func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethapi.FilterQuery, policy LogsSubscriptionPolicy) (*LogsSubscription, error) {
	filter, err := compileLogsFilter(q)
	if err != nil {
		return nil, err
	}
	if err := validateLogsPolicy(policy); err != nil {
		return nil, err
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	return startLogsSubscription(c.lifeCtx, ctx, sources, filter, policy, c.Mode())
}

// decodeReceiptNotification is shared by single-source events, aggregated
// events, deliveries and same-source Pull. Official checked bytes determine
// the candidate key; the generated values are only the public projection.
func decodeReceiptNotification(raw []byte, sourceID string) (internalsubscription.Observation, []ethapi.Receipt, error) {
	values, canonical, err := typed.DecodeReceiptBatch(raw)
	if err != nil {
		return internalsubscription.Observation{}, nil, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(canonical, &rows); err != nil {
		return internalsubscription.Observation{}, nil, err
	}
	identities, err := receiptBatchIdentities(values, rows)
	if err != nil {
		return internalsubscription.Observation{}, nil, err
	}
	contextKey, key, err := internalsubscription.CanonicalTransactionReceiptsKey(identities)
	if err != nil {
		return internalsubscription.Observation{}, nil, err
	}
	return internalsubscription.Observation{Kind: internalsubscription.KindTransactionReceipts, ContextKey: contextKey, Key: key, SourceID: sourceID, Payload: append([]byte(nil), raw...)}, values, nil
}

// receiptBatchIdentities checks the batch before Push voting or Pull filtering.
// Indices may be noncontiguous: one notification can contain a filtered subset.
func receiptBatchIdentities(values []ethapi.Receipt, rows []json.RawMessage) ([]internalsubscription.ReceiptIdentity, error) {
	identities := make([]internalsubscription.ReceiptIdentity, len(values))
	hashes := make(map[ethapi.Hash]struct{}, len(values))
	indices := make(map[uint64]struct{}, len(values))
	var firstHeight uint64
	for i := range values {
		height, heightErr := typed.QuantityHeight(values[i].BlockNumber)
		if heightErr != nil {
			return nil, fmt.Errorf("ethguard: receipt %d has no block identity", i)
		}
		index, indexErr := typed.QuantityHeight(values[i].TransactionIndex)
		if indexErr != nil {
			return nil, fmt.Errorf("ethguard: receipt %d has no transaction index", i)
		}
		if i == 0 {
			firstHeight = height
		} else if values[i].BlockHash != values[0].BlockHash || height != firstHeight {
			return nil, fmt.Errorf("ethguard: receipt %d has inconsistent block identity", i)
		}
		if _, exists := hashes[values[i].TransactionHash]; exists {
			return nil, fmt.Errorf("ethguard: receipt %d repeats transaction hash %s", i, values[i].TransactionHash)
		}
		if _, exists := indices[index]; exists {
			return nil, fmt.Errorf("ethguard: receipt %d repeats transaction index %d", i, index)
		}
		hashes[values[i].TransactionHash] = struct{}{}
		indices[index] = struct{}{}
		identities[i] = internalsubscription.ReceiptIdentity{
			TransactionHash: string(values[i].TransactionHash), BlockHash: string(values[i].BlockHash),
			BlockNumber: strconv.FormatUint(height, 10), TransactionIndex: index, Canonical: rows[i],
		}
	}
	return identities, nil
}

// snapshotReceiptSubscriptionQuery freezes the generated receipt-subscription
// query on a private copy. nil query, an unset list, and an empty list (with
// a directly constructed zero-length slice) all normalize to the empty
// official object — the wire key stays absent, so empty means all receipts.
// A non-empty list is re-validated hash by hash; an invalid hash is an input
// error that fails before the subscription is established — it never widens
// into the empty "all receipts" query and never rides as a partial list.
// Only canonical spellings enter the frozen copy, so later caller mutation of
// the input cannot change the wire. This empty-means-all rule is the
// inherited project contract, not something the JSON schema derives.
func snapshotReceiptSubscriptionQuery(q *ethapi.TransactionReceiptsQuery) (*ethapi.TransactionReceiptsQuery, ethapi.TransactionReceiptsQuery, error) {
	frozen := &ethapi.TransactionReceiptsQuery{}
	var wire ethapi.TransactionReceiptsQuery
	if q != nil && q.TransactionHashes != nil && len(*q.TransactionHashes) != 0 {
		hashes := make([]ethapi.Hash, 0, len(*q.TransactionHashes))
		for _, hash := range *q.TransactionHashes {
			canonical, err := validateHash(hash)
			if err != nil {
				return nil, ethapi.TransactionReceiptsQuery{}, fmt.Errorf("ethguard: receipt subscription filter hash %q: %w", string(hash), ErrInvalidRequest)
			}
			hashes = append(hashes, canonical)
		}
		frozen.TransactionHashes = &hashes
		wire.TransactionHashes = &hashes
	}
	return frozen, wire, nil
}

func (c *Client) SubscribeTransactionReceipts(ctx context.Context, q *ethapi.TransactionReceiptsQuery, ch chan<- []ethapi.Receipt) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	// The filter is validated and frozen before any source selection or
	// registration: an invalid filter establishes nothing on any surface.
	query, wireQuery, err := snapshotReceiptSubscriptionQuery(q)
	if err != nil {
		return nil, err
	}
	sources, err := c.subscriptionSources()
	if err != nil {
		return nil, err
	}
	wanted := make(map[ethapi.Hash]struct{})
	if query.TransactionHashes != nil {
		for _, hash := range *query.TransactionHashes {
			wanted[hash] = struct{}{}
		}
	}
	decode := func(raw []byte, sourceID string) (internalsubscription.Observation, []ethapi.Receipt, error) {
		observation, values, err := decodeReceiptNotification(raw, sourceID)
		if err != nil {
			return internalsubscription.Observation{}, nil, err
		}
		if len(wanted) != 0 {
			for i, receipt := range values {
				if _, matches := wanted[receipt.TransactionHash]; !matches {
					return internalsubscription.Observation{}, nil, fmt.Errorf("ethguard: receipt %d transaction %s does not match subscription filter", i, receipt.TransactionHash)
				}
			}
		}
		return observation, values, nil
	}
	if len(sources) == 1 {
		return subscribeSingleSource(ctx, c.lifeCtx, sources[0], "transactionReceipts", []any{wireQuery}, ch, func(raw []byte) ([]ethapi.Receipt, error) {
			_, values, err := decode(raw, sources[0].ID())
			return values, err
		})
	}

	return startAggregatedSubscriptionWithPullOwner(c.lifeCtx, ctx, sources, internalsubscription.KindTransactionReceipts, []any{wireQuery}, ch, decode, func(pctx context.Context, source identifiedSubscriptionSource, kind internalsubscription.Kind, contextKey string, seed internalsubscription.Observation) (internalsubscription.Observation, error) {
		return pullReceiptNotification(pctx, source, kind, contextKey, seed, query)
	})
}

func pullReceiptNotification(pctx context.Context, source identifiedSubscriptionSource, kind internalsubscription.Kind, contextKey string, seed internalsubscription.Observation, query *ethapi.TransactionReceiptsQuery) (internalsubscription.Observation, error) {
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
	for i := range seedReceipts {
		if seedReceipts[i].BlockHash != blockHash {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: receipt %d has inconsistent block identity", i)
		}
	}
	raw, callErr := unary.CallContext(pctx, "eth_getBlockReceipts", string(blockHash))
	if callErr != nil {
		return internalsubscription.Observation{}, callErr
	}
	allReceipts, canonical, decodeErr := typed.DecodeReceiptBatch(raw)
	if decodeErr != nil {
		return internalsubscription.Observation{}, decodeErr
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(canonical, &rows); err != nil {
		return internalsubscription.Observation{}, err
	}
	if _, err := receiptBatchIdentities(allReceipts, rows); err != nil {
		return internalsubscription.Observation{}, err
	}
	if len(allReceipts) > 0 && (allReceipts[0].BlockHash != blockHash || allReceipts[0].BlockNumber != seedReceipts[0].BlockNumber) {
		return internalsubscription.Observation{}, fmt.Errorf("ethguard: block receipt Pull has inconsistent seeded block identity")
	}
	wanted := make(map[string]struct{})
	if query != nil && query.TransactionHashes != nil {
		for _, hash := range *query.TransactionHashes {
			wanted[string(hash)] = struct{}{}
		}
	}
	seedHashes := make(map[string]struct{}, len(seedReceipts))
	for i := range seedReceipts {
		seedHashes[string(seedReceipts[i].TransactionHash)] = struct{}{}
	}
	filtered := make([]ethapi.Receipt, 0, len(allReceipts))
	filteredRows := make([]json.RawMessage, 0, len(allReceipts))
	for i := range allReceipts {
		if len(wanted) == 0 {
			filtered = append(filtered, allReceipts[i])
			filteredRows = append(filteredRows, rows[i])
			continue
		}
		if _, ok := wanted[string(allReceipts[i].TransactionHash)]; ok {
			filtered = append(filtered, allReceipts[i])
			filteredRows = append(filteredRows, rows[i])
		}
	}
	for i := range filtered {
		delete(seedHashes, string(filtered[i].TransactionHash))
	}
	if len(seedHashes) > 0 {
		for hash := range seedHashes {
			return internalsubscription.Observation{}, fmt.Errorf("ethguard: block receipt Pull omitted seeded transaction %s", hash)
		}
	}
	if len(filtered) == 0 {
		return internalsubscription.Observation{}, fmt.Errorf("ethguard: block receipt Pull returned no matching receipts")
	}
	batch, marshalErr := json.Marshal(filteredRows)
	if marshalErr != nil {
		return internalsubscription.Observation{}, marshalErr
	}
	observation, _, decodeErr := decodeReceiptNotification(batch, source.ID())
	if decodeErr != nil {
		return internalsubscription.Observation{}, decodeErr
	}
	if observation.ContextKey != contextKey {
		return internalsubscription.Observation{}, fmt.Errorf("ethguard: receipt Pull context %q does not match %q", observation.ContextKey, contextKey)
	}
	observation.Generation = seed.Generation
	return observation, nil
}

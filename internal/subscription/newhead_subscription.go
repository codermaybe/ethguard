package subscription

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// HeadSubscriptionPolicy has no implicit resource or business-time defaults.
// MemoryBytes, RetainedNodes and MaxConcurrentRequests must be positive.
// Zero durations disable that deadline/TTL; RetryDelay zero disables retries.
// MaxRecoveryAttempts zero disables the attempt limit (the memory limit remains).
type HeadSubscriptionPolicy struct {
	MemoryBytes                                                       uint64
	WireMessages                                                      int
	WireBytes, MaxMessageBytes                                        uint64
	RetainedNodes, MaxConcurrentRequests                              int
	RequestTimeout, RecoveryTimeout, AuditTimeout, TipTTL, RetryDelay time.Duration
	MaxRecoveryAttempts                                               uint64
	Gate                                                              HeadGatePolicy
}
type HeadCommittedSample struct {
	Node          AnchorNode
	ArrivalSpread time.Duration
	Sources       int
}

// Estimate runs outside all core locks and sees only committed, same-hash
// observations. Initial is retained until a sample from at least two sources.
// The callback must return promptly and must not call this subscription's Close.
type HeadGatePolicy struct {
	Initial, Min, Max time.Duration
	Estimate          func(HeadCommittedSample) time.Duration
}
type HeadDelivery struct {
	Node           AnchorNode
	Header         *ethapi.Header
	ObjectSupplier ObjectSupplier
	Tier           EvidenceTier
}
type HeadPathDelta struct {
	BaseVersion, NewVersion uint64
	CommonAncestor          AnchorNode
	Retracted               []AnchorNode
	Appended                []HeadDelivery
	PreBirthProof           []AnchorNode
	Reason                  PathChangeReason
}
type HeadEvent struct {
	Path       HeadPathDelta
	Evaluation EvaluationSnapshot
	Audit      AuditRange
}
type headFetchTarget struct {
	source  Source
	session SourceSession
}
type headFetch struct {
	request      ObjectRequest
	targets      []headFetchTarget
	next, active int
	ctx          context.Context
	cancel       context.CancelFunc
	done         bool
	release      func()
}

// HeadSubscription owns all work for one stream. Next transfers event ownership;
// active Close discards queued events and joins every network operation.
type HeadSubscription struct {
	life            context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	terminated      chan struct{}
	terminalCause   error
	watcherDone     chan struct{}
	queue           *EventQueue
	budget          *AtomicMemoryBudget
	arena           *PayloadArena
	tracker         *HeadTracker
	sessions        *SessionGate
	sources         []Source
	policy          HeadSubscriptionPolicy
	mu              sync.Mutex
	wg              sync.WaitGroup
	stopping        bool
	fetches         []*headFetch
	activeRequests  int
	wake            chan struct{}
	gateDuration    time.Duration
	gateDeadline    time.Time
	sourceProducers int
	sample          *HeadCommittedSample
	sampleWake      chan struct{}
	fixedRelease    func()
}

var headDomainSequence atomic.Uint64

func (s *HeadSubscription) Basis() EvidenceBasis {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tracker.Basis()
}

func (s *HeadSubscription) Next(ctx context.Context) (HeadEvent, error) {
	event, _, err := s.nextTyped(ctx, nil)
	return event, err
}

// NextWithBudget transfers a complete event or terminal to destination before
// releasing source accounting. The reservation includes the typed delivery
// projection. Retain release until the event/error is discarded or externally
// delivered. Reserve must be nonblocking and must not synchronously call Close.
// A failed transfer returns only audit under its source reservation, owned by
// release; the unread payload remains in the native queue.
func (s *HeadSubscription) NextWithBudget(ctx context.Context, destination MemoryBudget) (HeadEvent, func(), error) {
	if destination == nil {
		return HeadEvent{}, nil, fmt.Errorf("subscription: missing destination budget")
	}
	return s.nextTyped(ctx, headTransferBudget{destination})
}

type headTransferBudget struct{ MemoryBudget }

func (b headTransferBudget) Reserve(n uint64) (func(), error) {
	if n > (^uint64(0)-4096)/2 {
		return nil, ErrMemoryBudget
	}
	return b.MemoryBudget.Reserve(2*n + 4096)
}
func (s *HeadSubscription) nextTyped(ctx context.Context, destination MemoryBudget) (HeadEvent, func(), error) {
	e, release, err := s.queue.next(ctx, destination)
	if err != nil {
		if ctx.Err() != nil {
			_ = s.Close()
		}
		return HeadEvent{}, release, err
	}
	if e.Delta == nil {
		if release != nil {
			release()
		}
		return HeadEvent{}, nil, fmt.Errorf("ethguard: missing head path delta")
	}
	d := e.Delta
	out := HeadEvent{Path: HeadPathDelta{BaseVersion: d.BaseVersion, NewVersion: d.NewVersion, CommonAncestor: d.CommonAncestor, Retracted: d.Retracted, PreBirthProof: d.PreBirthProof, Reason: d.Reason, Appended: make([]HeadDelivery, len(d.Appended))}, Evaluation: e.Evaluation, Audit: e.Audit}
	for i, a := range d.Appended {
		h, ok := a.HeaderValue.(*ethapi.Header)
		if !ok {
			if release != nil {
				release()
			}
			return HeadEvent{}, nil, fmt.Errorf("ethguard: missing admitted header")
		}
		out.Path.Appended[i] = HeadDelivery{Node: a.Node, Header: h, ObjectSupplier: a.ObjectSupplier, Tier: e.EvidenceTier}
	}
	return out, release, nil
}

// Terminated closes at the producer terminal cut, independently of queued
// delivery and cleanup. Next retains its FIFO drain and full audit contract.
func (s *HeadSubscription) Terminated() <-chan struct{} { return s.terminated }

// TerminalError reports the stable producer cause after Terminated closes.
// Explicit cancellation has no failure cause. Complete audit is available via Next.
func (s *HeadSubscription) TerminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalCause
}
func (s *HeadSubscription) abort() {
	s.mu.Lock()
	// Publish intentional termination before waking queue consumers. A bridge
	// must never observe ErrClosed while the stable terminal cause is undecided.
	s.finishLocked(nil)
	// An already failed producer still needs its unread FIFO discarded.
	s.queue.Abort()
	s.mu.Unlock()
}
func (s *HeadSubscription) Close() error {
	s.abort()
	// Join only the in-flight ownership transfer. Cleanup waits below must
	// remain outside this mutex so concurrent Close callers can join together.
	s.queue.nextMu.Lock()
	s.queue.nextMu.Unlock() //nolint:staticcheck // Intentional empty critical section: join the current Next transfer.
	<-s.done
	<-s.watcherDone
	return nil
}
func (s *HeadSubscription) launchLocked(fn func()) {
	s.wg.Add(1)
	go func() { defer s.wg.Done(); fn() }()
}
func (s *HeadSubscription) finishLocked(err error) {
	if s.stopping {
		return
	}
	// Owner cancellation may reach a source before the owner watcher obtains
	// this lock. Visible cancellation owns the stop; it cannot become a new
	// producer failure merely because the source runs first.
	if s.life.Err() != nil {
		err = nil
	}
	s.stopping = true
	s.terminalCause = stableTerminalCause(err)
	close(s.terminated)
	s.cancel()
	for _, f := range s.fetches {
		f.cancel()
	}
	if err != nil {
		_ = s.queue.Close(s.tracker.Terminal(err))
	} else {
		s.queue.Abort()
	}
	go s.reapWork()
}

// reapWork joins only actual producers and requests. Unread delivery belongs
// to queue; its owner watcher remains independently alive until drain/abort.
func (s *HeadSubscription) reapWork() {
	s.wg.Wait()
	s.mu.Lock()
	for _, f := range s.fetches {
		f.release()
		f.targets = nil
	}
	s.fetches = nil
	s.tracker.Close()
	s.sessions.Close()
	s.sources = nil
	s.sample = nil
	s.fixedRelease()
	s.fixedRelease = nil
	s.mu.Unlock()
	close(s.done)
}
func validateHeadPolicy(p HeadSubscriptionPolicy) error {
	if p.WireMessages <= 0 || p.MaxMessageBytes == 0 || p.WireBytes < p.MaxMessageBytes || p.MemoryBytes < p.WireBytes {
		return fmt.Errorf("%w: positive wire limits with message <= wire <= memory required", rpc.ErrInvalidRequest)
	}
	if p.MemoryBytes == 0 || p.RetainedNodes <= 0 || p.MaxConcurrentRequests <= 0 {
		return fmt.Errorf("%w: positive head memory, retention and concurrency required", rpc.ErrInvalidRequest)
	}
	if p.RequestTimeout < 0 || p.RecoveryTimeout < 0 || p.AuditTimeout < 0 || p.TipTTL < 0 || p.RetryDelay < 0 || p.Gate.Initial < 0 || p.Gate.Min < 0 || p.Gate.Max < 0 {
		return fmt.Errorf("%w: negative head policy duration", rpc.ErrInvalidRequest)
	}
	g := p.Gate
	if g.Initial == 0 {
		if g.Min != 0 || g.Max != 0 || g.Estimate != nil {
			return fmt.Errorf("%w: enabled gate requires an initial duration", rpc.ErrInvalidRequest)
		}
	} else if g.Min <= 0 || g.Max < g.Min || g.Initial < g.Min || g.Initial > g.Max {
		return fmt.Errorf("%w: invalid head gate bounds", rpc.ErrInvalidRequest)
	}
	return nil
}
func startNewHeadSubscription(ownerCtx, setupCtx context.Context, sources []Source, policy HeadSubscriptionPolicy, mode rpc.CorroborationMode) (*HeadSubscription, error) {
	if err := validateHeadPolicy(policy); err != nil {
		return nil, err
	}
	if err := setupCtx.Err(); err != nil {
		return nil, err
	}
	m := StrictMode
	if mode == rpc.CorroborationForwarded {
		m = ForwardedMode
	}
	members := make([]string, len(sources))
	for i, p := range sources {
		members[i] = p.ID()
	}
	budget := NewMemoryBudget(policy.MemoryBytes)
	// Admit fixed source/session slots, owned control channels and task control
	// allowance before creating the subscription's goroutines.
	fixedBytes, err := headSubscriptionFixedBytes(uint64(len(sources)))
	if err != nil {
		return nil, err
	}
	fixedRelease, err := budget.Reserve(fixedBytes)
	if err != nil {
		return nil, err
	}
	tracker, err := NewHeadTracker(HeadTrackerConfig{DomainID: fmt.Sprintf("newHeads/%d", headDomainSequence.Add(1)), Members: members, Quorum: len(members)/2 + 1, Mode: m, RetainedNodes: policy.RetainedNodes, Budget: budget, TipTTL: policy.TipTTL, RecoveryTimeout: policy.RecoveryTimeout, AuditTimeout: policy.AuditTimeout, MaxRecoveryAttempts: policy.MaxRecoveryAttempts})
	if err != nil {
		fixedRelease()
		return nil, err
	}
	sessions, err := NewSessionGate(members)
	if err != nil {
		tracker.Close()
		fixedRelease()
		return nil, err
	}
	life, cancel := context.WithCancel(ownerCtx)
	s := &HeadSubscription{life: life, cancel: cancel, done: make(chan struct{}), terminated: make(chan struct{}), watcherDone: make(chan struct{}), queue: NewEventQueue(budget), budget: budget, arena: NewPayloadArena(budget), tracker: tracker, sessions: sessions, sources: append([]Source(nil), sources...), policy: policy, wake: make(chan struct{}, 1), gateDuration: policy.Gate.Initial, sourceProducers: len(sources), sampleWake: make(chan struct{}, 1), fixedRelease: fixedRelease}
	s.mu.Lock()
	for _, source := range sources {
		s.launchLocked(func() { s.runSource(source) })
	}
	s.launchLocked(s.runClock)
	if policy.Gate.Estimate != nil {
		s.launchLocked(s.runEstimator)
	}
	s.mu.Unlock()
	go func() {
		defer close(s.watcherDone)
		select {
		case <-ownerCtx.Done():
			s.abort()
		case <-s.queue.Done():
		}
	}()
	return s, nil
}

func headSubscriptionFixedBytes(sources uint64) (uint64, error) {
	const (
		controlBytes = uint64(16384) // Owned control channels and shared task bookkeeping.
		sourceBytes  = uint64(4608)  // Source/session slots and producer task allowance per member.
	)
	bytes := uint64(unsafe.Sizeof(HeadSubscription{})) + controlBytes
	if sources > (^uint64(0)-bytes)/sourceBytes {
		return 0, ErrMemoryBudget
	}
	return bytes + sources*sourceBytes, nil
}

func (s *HeadSubscription) runSource(source Source) {
	exhausted := false
	exhaust := func() {
		if exhausted {
			return
		}
		exhausted = true
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sourceProducers--
		if s.sourceProducers == 0 && !s.stopping {
			s.finishLocked(fmt.Errorf("ethguard: all head source sessions ended"))
		}
	}
	defer exhaust()
	for {
		session, admitted := s.admitSourceSession(source.ID())
		if !admitted {
			return
		}
		var inner transport.Subscription
		var err error
		if bounded, ok := source.(transport.BoundedSubscriber); ok {
			inner, err = bounded.SubscribeBounded(s.life, transport.NotificationPolicy{Messages: s.policy.WireMessages, Bytes: s.policy.WireBytes, MaxMessageBytes: s.policy.MaxMessageBytes, Reserve: s.budget.Reserve}, "newHeads")
		} else {
			err = fmt.Errorf("head source %s lacks bounded subscription capability", source.ID())
		}
		setupFailed := err != nil
		if err == nil {
			err = s.life.Err()
			if err == nil {
				err = s.readSource(session, inner)
			}
		}
		s.disconnectNow(session, err, setupFailed)
		s.mu.Lock()
		retry := !s.stopping && s.policy.RetryDelay > 0
		s.mu.Unlock()
		// Exhaustion closes production before potentially slow network cleanup.
		// This source goroutine alone owns the inner and calls Unsubscribe once.
		if !retry {
			exhaust()
		}
		if inner != nil {
			inner.Unsubscribe()
		}
		if !retry || !waitSubscriptionRetry(s.life.Done(), s.life, s.policy.RetryDelay) {
			return
		}
	}
}

// admitSourceSession serializes stopping, generation opening and tracker
// binding. Once admitted, an in-flight Subscribe may finish after cancellation;
// runSource still owns that returned inner and joins its cleanup.
func (s *HeadSubscription) admitSourceSession(sourceID string) (SourceSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return SourceSession{}, false
	}
	if s.life.Err() != nil {
		s.finishLocked(nil)
		return SourceSession{}, false
	}
	session, err := s.sessions.Open(sourceID)
	if err == nil {
		err = s.tracker.Bind(session, time.Now())
	}
	if err != nil {
		s.sessions.Retire(session)
		s.finishLocked(err)
		return SourceSession{}, false
	}
	s.driveLocked(time.Now())
	if s.stopping {
		s.sessions.Retire(session)
		return SourceSession{}, false
	}
	return session, true
}
func (s *HeadSubscription) disconnectNow(session SourceSession, cause error, setup bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Retire content suppliers before cleanup can block. The gate's generation
	// check prevents a late disconnect from invalidating a replacement session.
	s.sessions.Retire(session)
	if s.stopping {
		return
	}
	if s.life.Err() != nil {
		s.finishLocked(nil)
		return
	}
	if errors.Is(cause, ErrMemoryBudget) {
		s.finishLocked(cause)
		return
	}
	reason := ReasonDisconnected
	switch {
	case errors.Is(cause, transport.ErrBodyTooLarge):
		reason = ReasonMessageTooLarge
	case errors.Is(cause, transport.ErrNotificationBackpressure):
		reason = ReasonNotificationLimit
	case setup:
		reason = ReasonSourceSetupFailure
	}
	if err := s.tracker.Disconnect(session, time.Now(), reason); err != nil {
		s.finishLocked(err)
		return
	}
	s.driveLocked(time.Now())
}
func (s *HeadSubscription) readSource(session SourceSession, inner transport.Subscription) error {
	events, errs := inner.Events(), inner.Err()
	var sequence uint64
	var terminal error
	for events != nil || errs != nil {
		select {
		case <-s.life.Done():
			return s.life.Err()
		case err, ok := <-errs:
			if !ok {
				errs = nil
			} else if err != nil {
				terminal = err
				errs = nil
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if (event.SourceID != "" && event.SourceID != session.SourceID) || (event.Generation != 0 && event.Generation != session.Generation) {
				if event.Release != nil {
					event.Release()
				}
				continue
			}
			sequence++
			node, lease, decodeErr := s.decode(event.Result)
			if event.Release != nil {
				event.Release()
				event.Release = nil
			}
			event.Raw = nil
			event.Result = nil
			s.mu.Lock()
			if s.stopping || s.life.Err() != nil {
				lease.Release()
				s.finishLocked(nil)
				s.mu.Unlock()
				return s.life.Err()
			}
			now := time.Now()
			var err error
			if decodeErr != nil {
				if errors.Is(decodeErr, ErrMemoryBudget) {
					err = decodeErr
				} else {
					reason := ReasonInvalidPush
					if errors.Is(decodeErr, transport.ErrBodyTooLarge) {
						reason = ReasonMessageTooLarge
					}
					err = s.tracker.Reject(session, sequence, now, reason)
				}
			} else {
				err = s.tracker.Observe(TipObservation{Session: session, ReadSequence: sequence, Node: node, Header: lease}, now)
				if errors.Is(err, ErrInvalidCandidate) {
					err = s.tracker.Reject(session, sequence, now)
				}
			}
			if err != nil && !errors.Is(err, ErrSessionStale) && !errors.Is(err, ErrReadSequence) {
				s.finishLocked(err)
			} else {
				s.driveLocked(now)
			}
			s.mu.Unlock()
		}
	}
	return terminal
}
func (s *HeadSubscription) decode(raw []byte) (AnchorNode, PayloadLease, error) {
	if uint64(len(raw)) > s.policy.MaxMessageBytes {
		return AnchorNode{}, PayloadLease{}, transport.ErrBodyTooLarge
	}
	if uint64(len(raw)) > (^uint64(0)-(16<<10))/256 {
		return AnchorNode{}, PayloadLease{}, ErrMemoryBudget
	}
	// JSON token/maps, string decoding, canonical projections and hash bridge
	// use more space than wire bytes. Charge a conservative structural allowance
	// before parsing; this is allocation accounting, not a Go heap/RSS limit.
	release, err := s.budget.Reserve(uint64(len(raw))*256 + 16<<10)
	if err != nil {
		return AnchorNode{}, PayloadLease{}, err
	}
	defer release()
	h, err := typed.DecodeHeader(raw)
	if err != nil {
		return AnchorNode{}, PayloadLease{}, err
	}
	node, err := headerNode(h)
	if err != nil {
		return node, PayloadLease{}, err
	}
	p, err := s.arena.Store(h, uint64(len(raw))*2+uint64(unsafe.Sizeof(*h)))
	return node, p, err
}
func (s *HeadSubscription) driveLocked(now time.Time) {
	if s.stopping {
		return
	}
	for {
		p, r, err := s.tracker.Prepare(now)
		if err != nil {
			s.finishLocked(err)
			return
		}
		if p == nil {
			if r != nil {
				s.startFetchLocked(*r, now)
			}
			break
		}
		target := *p.Event.Evaluation.Target
		spread, count := s.tracker.ArrivalSample(target)
		if err = s.tracker.Publish(s.queue, p); err != nil {
			s.finishLocked(err)
			return
		}
		s.gateDeadline = time.Time{}
		if s.policy.Gate.Estimate != nil && count >= 2 {
			s.sample = &HeadCommittedSample{Node: target, ArrivalSpread: spread, Sources: count}
			select {
			case s.sampleWake <- struct{}{}:
			default:
			}
		}
	}
	if s.gateDuration > 0 && s.tracker.PendingAudit() > 0 && s.gateDeadline.IsZero() {
		s.gateDeadline = now.Add(s.gateDuration)
	}
	s.pumpFetchLocked(now)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *HeadSubscription) startFetchLocked(r ObjectRequest, now time.Time) {
	bytes := uint64(len(s.sources))*uint64(unsafe.Sizeof(headFetchTarget{})) + uint64(unsafe.Sizeof(headFetch{})) + 256
	release, err := s.budget.Reserve(bytes)
	if err != nil {
		s.finishLocked(err)
		return
	}
	ctx, cancel := context.WithCancel(s.life)
	f := &headFetch{request: r, ctx: ctx, cancel: cancel, release: release}
	for _, source := range s.sources {
		session, err := s.sessions.Current(source.ID())
		if err == nil {
			f.targets = append(f.targets, headFetchTarget{source, session})
		}
	}
	s.fetches = append(s.fetches, f)
}
func (s *HeadSubscription) pumpFetchLocked(now time.Time) {
	if s.stopping {
		return
	}
	for _, f := range s.fetches {
		if !f.done && !s.tracker.RequestPending(f.request) {
			f.done = true
			f.cancel()
		}
		if f.done {
			continue
		}
		for f.next < len(f.targets) && s.activeRequests < s.policy.MaxConcurrentRequests {
			target := f.targets[f.next]
			f.next++
			f.active++
			s.activeRequests++
			s.launchLocked(func() { s.fetchOne(f, target) })
		}
		if f.active == 0 && f.next == len(f.targets) {
			if err := s.tracker.ExhaustRequest(f.request, now, s.policy.RetryDelay); err != nil {
				s.finishLocked(err)
				return
			}
			f.done = true
			f.cancel()
		}
	}
	// Completed/abandoned groups never retain their sources or cancellation closures.
	out := s.fetches[:0]
	for _, f := range s.fetches {
		if f.done && f.active == 0 {
			f.release()
			f.targets = nil
		} else {
			out = append(out, f)
		}
	}
	clear(s.fetches[len(out):])
	if len(out) == 0 {
		s.fetches = nil
	} else if cap(out)/4 > len(out) {
		s.fetches = append([]*headFetch(nil), out...)
	} else {
		s.fetches = out
	}
}
func (s *HeadSubscription) fetchOne(f *headFetch, target headFetchTarget) {
	ctx := f.ctx
	cancel := func() {}
	if s.policy.RequestTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.policy.RequestTimeout)
	}
	defer cancel()
	var node AnchorNode
	var lease PayloadLease
	var err error
	reason := ReasonRequestFailure
	unary, ok := target.source.(subscriptionUnarySource)
	if !ok {
		err = ErrUnknownSource
	} else {
		var raw []byte
		raw, err = unary.CallContext(ctx, "eth_getBlockByHash", "0x"+hex.EncodeToString(f.request.Hash[:]), false)
		if err == nil {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				reason = ReasonNullObject
			} else {
				reason = ReasonInvalidObject
			}
			node, lease, err = s.decode(raw)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeRequests--
	f.active--
	if s.stopping || f.done {
		lease.Release()
		s.pumpFetchLocked(time.Now())
		return
	}
	now := time.Now()
	if err2 := s.sessions.Accept(target.session); err2 != nil {
		lease.Release()
		lease = PayloadLease{}
		err = err2
	}
	if errors.Is(err, ErrMemoryBudget) {
		lease.Release()
		s.finishLocked(err)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		reason = ReasonRequestTimeout
	}
	acceptErr := s.tracker.AcceptObject(ObjectResult{Request: f.request, Session: target.session, Node: node, Header: lease, Err: err, Reason: reason}, now)
	if acceptErr != nil && !errors.Is(acceptErr, ErrSessionStale) && !errors.Is(acceptErr, ErrStaleAttempt) {
		s.finishLocked(acceptErr)
		return
	}
	if !s.tracker.RequestPending(f.request) {
		f.done = true
		f.cancel()
	}
	s.driveLocked(now)
}
func (s *HeadSubscription) runClock() {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			return
		}
		deadline := s.tracker.NextDeadline()
		include := func(t time.Time) {
			if !t.IsZero() && (deadline.IsZero() || t.Before(deadline)) {
				deadline = t
			}
		}
		include(s.gateDeadline)
		s.mu.Unlock()
		var tick <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d < 0 {
				d = 0
			}
			timer.Reset(d)
			tick = timer.C
		}
		select {
		case <-s.life.Done():
			return
		case <-s.wake:
			if tick != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case now := <-tick:
			s.mu.Lock()
			if !s.stopping {
				if !s.gateDeadline.IsZero() && !now.Before(s.gateDeadline) {
					if err := s.tracker.GateExpired(now); err != nil {
						s.finishLocked(err)
					}
					s.gateDeadline = time.Time{}
				}
				s.driveLocked(now)
			}
			s.mu.Unlock()
		}
	}
}
func (s *HeadSubscription) runEstimator() {
	for {
		select {
		case <-s.life.Done():
			return
		case <-s.sampleWake:
			s.mu.Lock()
			if s.stopping || s.life.Err() != nil {
				s.mu.Unlock()
				return
			}
			sample := s.sample
			s.sample = nil
			s.mu.Unlock()
			if sample == nil {
				continue
			}
			gate := s.policy.Gate.Estimate(*sample)
			if gate < s.policy.Gate.Min {
				gate = s.policy.Gate.Min
			}
			if gate > s.policy.Gate.Max {
				gate = s.policy.Gate.Max
			}
			s.mu.Lock()
			if !s.stopping {
				s.gateDuration = gate
			}
			s.mu.Unlock()
		}
	}
}
func headerNode(h *ethapi.Header) (AnchorNode, error) {
	n, err := typed.QuantityHeight(h.Number)
	if err != nil {
		return AnchorNode{}, err
	}
	hash, err := decodeHeaderHash(string(h.Hash))
	if err != nil {
		return AnchorNode{}, err
	}
	parent, err := decodeHeaderHash(string(h.ParentHash))
	return AnchorNode{Number: n, Hash: hash, ParentHash: parent}, err
}
func decodeHeaderHash(value string) ([32]byte, error) {
	var h [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(b) != 32 {
		return h, fmt.Errorf("invalid header hash")
	}
	copy(h[:], b)
	return h, nil
}

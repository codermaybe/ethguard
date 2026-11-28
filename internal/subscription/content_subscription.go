package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/codermaybe/ethguard/ethapi"
	objectheader "github.com/codermaybe/ethguard/internal/ethobject/header"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/core/types"
)

type ContentReason uint8

const (
	ContentRequestFailure ContentReason = iota
	ContentInvalid
	ContentRequestTimeout
	ContentResponseTooLarge
	ContentStaleSession
	ContentRequestCanceled
	ContentRequestUnfinished
	ContentVerified
)

type ContentRequestKind uint8

const (
	ContentLogProof ContentRequestKind = iota
	ContentTransactions
	ContentReceipts
)

// ContentAudit records one bounded completion; it retains no raw answer or
// provider error string. Request identity includes its block and attempt.
type ContentAudit struct {
	Kind                         ContentRequestKind
	Sequence, Attempt, RequestID uint64
	Session                      SourceSession
	Block                        AnchorNode
	Reason                       ContentReason
	At                           int64
}
type contentTerminalError struct {
	Cause        error
	CommittedTip AnchorNode
	PathVersion  uint64
	Audit        AuditRange
	ContentAudit []ContentAudit
}

func (e *contentTerminalError) Error() string {
	return fmt.Sprintf("content subscription terminated: %v", e.Cause)
}
func (e *contentTerminalError) Unwrap() error { return e.Cause }

type queuedContentEvent[E any] struct {
	event    E
	audit    []ContentAudit
	releases []func()
	next     *queuedContentEvent[E]
}

// contentSubscription owns the shared delivery and work resources of the two
// block-content streams. Its type parameter only gives the FIFO a typed value.
// Method-specific proofs, filtering, retry and quorum remain with each producer.
type contentSubscription[E any] struct {
	watcherDone                       chan struct{}
	fixedRelease                      func()
	life                              context.Context
	cancel                            context.CancelFunc
	done                              chan struct{}
	terminated                        chan struct{}
	terminalOnce                      sync.Once
	terminalCause                     error
	drained                           chan struct{}
	drainedOnce                       sync.Once
	head                              *HeadSubscription
	budget                            *AtomicMemoryBudget
	sources                           []Source
	slots                             chan struct{}
	wg                                sync.WaitGroup
	mu                                sync.Mutex
	nextMu                            sync.Mutex
	notify                            chan struct{}
	first, last                       *queuedContentEvent[E]
	closed, aborted, terminalReturned bool
	terminal                          error
	terminalReleases                  []func()
	terminalReserve                   func()
	committedTip                      AnchorNode
	pathVersion                       uint64
	auditSequence                     uint64
	requestSequence                   uint64
	active                            []*contentRequest
	auditFirst, auditLast             *contentAuditEntry
}

type contentAuditEntry struct {
	record  ContentAudit
	release func()
	next    *contentAuditEntry
}
type contentRequest struct {
	supplier     ObjectSupplier
	kind         ContentRequestKind
	attempt      uint64
	node         AnchorNode
	auditRelease func()
	release      func()
}

func newContentSubscription[E any](owner, setup context.Context, sources []Source, headPolicy HeadSubscriptionPolicy, memory uint64, concurrency int, extra uint64, mode rpc.CorroborationMode) (*contentSubscription[E], error) {
	if err := setup.Err(); err != nil {
		return nil, err
	}
	const (
		controlBytes     = uint64(16384) // Owned channels, queue control and task bookkeeping.
		sourceBytes      = uint64(512)   // Fixed content source/session bookkeeping per member.
		requestSlotBytes = uint64(512)   // Active request bookkeeping per admitted concurrency slot.
	)
	fixedBytes := uint64(unsafe.Sizeof(contentSubscription[E]{})) + controlBytes
	if uint64(len(sources)) > (^uint64(0)-fixedBytes)/sourceBytes {
		return nil, ErrMemoryBudget
	}
	fixedBytes += uint64(len(sources)) * sourceBytes
	if uint64(concurrency) > (^uint64(0)-fixedBytes)/requestSlotBytes {
		return nil, ErrMemoryBudget
	}
	fixedBytes += uint64(concurrency) * requestSlotBytes
	if extra > ^uint64(0)-fixedBytes {
		return nil, ErrMemoryBudget
	}
	budget := NewMemoryBudget(memory)
	fixed, err := budget.Reserve(fixedBytes + extra)
	if err != nil {
		return nil, err
	}
	terminalReserve, err := budget.Reserve(uint64(unsafe.Sizeof(contentTerminalError{})) + 256)
	if err != nil {
		fixed()
		return nil, err
	}
	life, cancel := context.WithCancel(owner)
	head, err := startNewHeadSubscription(life, setup, sources, headPolicy, mode)
	if err != nil {
		cancel()
		fixed()
		terminalReserve()
		return nil, err
	}
	s := &contentSubscription[E]{life: life, cancel: cancel, done: make(chan struct{}), terminated: make(chan struct{}), drained: make(chan struct{}), watcherDone: make(chan struct{}), head: head, budget: budget, sources: append([]Source(nil), sources...), slots: make(chan struct{}, concurrency), notify: make(chan struct{}, 1), terminalReserve: terminalReserve, fixedRelease: fixed}
	go func() {
		defer close(s.watcherDone)
		select {
		case <-owner.Done():
			s.abort()
		case <-s.drained:
		}
	}()
	return s, nil
}
func (s *contentSubscription[E]) Basis() EvidenceBasis { return s.head.Basis() }
func (s *contentSubscription[E]) CommittedTip() (AnchorNode, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committedTip, s.pathVersion
}

// joinWork runs after the concrete producer stops admitting requests. The
// owner watcher follows unread delivery and is joined separately by Close.
func (s *contentSubscription[E]) joinWork() {
	s.cancel()
	_ = s.head.Close()
	s.wg.Wait()
	s.sources = nil
	s.active = nil
	s.fixedRelease()
	s.fixedRelease = nil
}
func releaseContentOwnership(releases []func()) {
	for _, release := range releases {
		if release != nil {
			release()
		}
	}
}
func (s *contentSubscription[E]) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *contentSubscription[E]) abort() {
	s.mu.Lock()
	s.markTerminatedLocked(nil)
	s.aborted, s.closed = true, true
	s.cancel()
	for s.first != nil {
		item := s.first
		s.first = item.next
		releaseContentOwnership(item.releases)
		item.next = nil
	}
	s.last = nil
	releaseContentOwnership(s.terminalReleases)
	s.terminalReleases = nil
	s.terminal = nil
	s.drainedOnce.Do(func() { close(s.drained) })
	s.dropAuditLocked()
	if s.terminalReserve != nil {
		s.terminalReserve()
		s.terminalReserve = nil
	}
	s.mu.Unlock()
	s.signal()
}

// Terminated closes at the producer terminal cut without waiting for FIFO
// consumption or slow canceled workers. It does not replace Close's join.
func (s *contentSubscription[E]) Terminated() <-chan struct{} { return s.terminated }

// TerminalError is the stable cause after Terminated closes; nil denotes
// intentional cancellation. Next continues to own the full terminal audit.
func (s *contentSubscription[E]) TerminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalCause
}
func (s *contentSubscription[E]) markTerminatedLocked(err error) {
	s.terminalOnce.Do(func() { s.terminalCause = stableTerminalCause(err); close(s.terminated) })
}
func (s *contentSubscription[E]) Close() error {
	s.abort()
	// Join only the in-flight ownership transfer. Cleanup waits below must
	// remain outside this mutex so concurrent Close callers can join together.
	s.nextMu.Lock()
	s.nextMu.Unlock() //nolint:staticcheck // Intentional empty critical section: join the current Next transfer.
	<-s.done
	<-s.watcherDone
	return nil
}

// A destination failure leaves the full content event and audit with this
// FIFO. No event or audit escapes without a reservation. The caller may retry
// with another budget or Close to discard the unread ownership.
func (s *contentSubscription[E]) nextWithBudget(ctx context.Context, destination MemoryBudget) (E, []ContentAudit, func(), error) {
	var zero E
	s.nextMu.Lock()
	closeAfter := false
	defer func() {
		s.nextMu.Unlock()
		if closeAfter {
			_ = s.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			closeAfter = true
			return zero, nil, nil, err
		}
		s.mu.Lock()
		if s.first != nil {
			item := s.first
			s.first = item.next
			if s.first == nil {
				s.last = nil
			}
			item.next = nil
			s.mu.Unlock()
			var retained func()
			var err error
			if destination != nil {
				retained, err = destination.Reserve(saturatingAdd(transferValueBytes(item.event), transferValueBytes(item.audit)))
			}
			s.mu.Lock()
			aborted := s.aborted
			if err != nil && !aborted {
				item.next = s.first
				s.first = item
				if s.last == nil {
					s.last = item
				}
			}
			s.mu.Unlock()
			if aborted {
				releaseContentOwnership(item.releases)
				if retained != nil {
					retained()
				}
				return zero, nil, nil, ErrClosed
			}
			if err != nil {
				return zero, nil, nil, err
			}
			releaseContentOwnership(item.releases)
			return item.event, item.audit, retained, nil
		}
		if s.closed {
			if s.aborted {
				s.mu.Unlock()
				return zero, nil, nil, ErrClosed
			}
			if s.terminalReturned {
				s.mu.Unlock()
				return zero, nil, nil, ErrNextAfterTerminal
			}
			terminal, releases := s.terminal, s.terminalReleases
			s.terminal, s.terminalReleases = nil, nil
			s.mu.Unlock()
			var retained func()
			var err error
			if destination != nil {
				size := uint64(4096)
				var contentTerminal *contentTerminalError
				if errors.As(terminal, &contentTerminal) {
					size = saturatingAdd(size, saturatingAdd(transferValueBytes(contentTerminal.Audit), transferValueBytes(contentTerminal.ContentAudit)))
				}
				retained, err = destination.Reserve(size)
			}
			s.mu.Lock()
			aborted := s.aborted
			if err != nil && !aborted {
				s.terminal, s.terminalReleases = terminal, releases
			}
			if err == nil && !aborted {
				s.terminalReturned = true
				s.drainedOnce.Do(func() { close(s.drained) })
			}
			s.mu.Unlock()
			if aborted {
				releaseContentOwnership(releases)
				if retained != nil {
					retained()
				}
				return zero, nil, nil, ErrClosed
			}
			if err != nil {
				return zero, nil, nil, err
			}
			releaseContentOwnership(releases)
			return zero, nil, retained, terminal
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			closeAfter = true
			return zero, nil, nil, ctx.Err()
		case <-s.notify:
		}
	}
}

func (s *contentSubscription[E]) publish(event E, base, version uint64, tip AnchorNode, bytes uint64, releases []func()) error {
	reserved, err := s.budget.Reserve(uint64(unsafe.Sizeof(queuedContentEvent[E]{})) + bytes + uint64(cap(releases)+1)*8)
	if err != nil {
		return err
	}
	releases = append(releases, reserved)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		reserved()
		return ErrClosed
	}
	if base != s.pathVersion {
		s.mu.Unlock()
		reserved()
		return ErrStalePathVersion
	}
	audit, releases := s.takeAuditLocked(releases)
	item := &queuedContentEvent[E]{event: event, audit: audit, releases: releases}
	s.pathVersion, s.committedTip = version, tip
	if s.last == nil {
		s.first = item
	} else {
		s.last.next = item
	}
	s.last = item
	s.mu.Unlock()
	s.signal()
	return nil
}
func (s *contentSubscription[E]) finish(err error, audit AuditRange, content []ContentAudit, releases []func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		releaseContentOwnership(releases)
		return
	}
	s.closed = true
	if s.life.Err() != nil {
		err = nil
	}
	s.markTerminatedLocked(err)
	s.cancel()
	// The terminal cut precedes cleanup. Slow suppliers cannot hide the error.
	for _, request := range s.active {
		s.appendAuditLocked(request, ContentRequestUnfinished)
	}
	pending, releases := s.takeAuditLocked(releases)
	content = append(content, pending...)
	s.terminal = &contentTerminalError{Cause: err, CommittedTip: s.committedTip, PathVersion: s.pathVersion, Audit: audit, ContentAudit: content}
	s.terminalReleases = append(releases, s.terminalReserve)
	s.terminalReserve = nil
	s.mu.Unlock()
	s.signal()
}
func (s *contentSubscription[E]) appendAuditLocked(request *contentRequest, reason ContentReason) {
	if request.auditRelease == nil {
		return
	}
	s.auditSequence++
	entry := &contentAuditEntry{record: ContentAudit{Kind: request.kind, Sequence: s.auditSequence, Attempt: request.attempt, RequestID: request.supplier.RequestID, Session: request.supplier.Session, Block: request.node, Reason: reason, At: time.Now().UnixNano()}, release: request.auditRelease}
	request.auditRelease = nil
	if s.auditLast == nil {
		s.auditFirst = entry
	} else {
		s.auditLast.next = entry
	}
	s.auditLast = entry
}
func (s *contentSubscription[E]) takeAuditLocked(releases []func()) ([]ContentAudit, []func()) {
	var out []ContentAudit
	for entry := s.auditFirst; entry != nil; entry = entry.next {
		out = append(out, entry.record)
		releases = append(releases, entry.release)
	}
	s.auditFirst, s.auditLast = nil, nil
	return out, releases
}
func (s *contentSubscription[E]) dropAuditLocked() {
	for entry := s.auditFirst; entry != nil; entry = entry.next {
		entry.release()
	}
	s.auditFirst, s.auditLast = nil, nil
}

func (s *contentSubscription[E]) beginRequest(source Source, node AnchorNode, attempt uint64, kind ContentRequestKind) (*contentRequest, error) {
	release, err := s.budget.Reserve(uint64(unsafe.Sizeof(contentRequest{})) + 256)
	if err != nil {
		return nil, err
	}
	auditRelease, err := s.budget.Reserve(uint64(unsafe.Sizeof(contentAuditEntry{})) + 2*uint64(unsafe.Sizeof(ContentAudit{})) + 128)
	if err != nil {
		release()
		return nil, err
	}
	session, _ := s.head.sessions.Current(source.ID())
	if session.SourceID == "" {
		session.SourceID = source.ID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		release()
		auditRelease()
		return nil, ErrClosed
	}
	s.requestSequence++
	s.wg.Add(1)
	request := &contentRequest{kind: kind, supplier: ObjectSupplier{Session: session, Acquisition: ByHash, RequestID: s.requestSequence, TargetHash: node.Hash}, attempt: attempt, node: node, release: release, auditRelease: auditRelease}
	s.active = append(s.active, request)
	return request, nil
}

func (s *contentSubscription[E]) completeRequest(request *contentRequest, reason ContentReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.active {
		if item == request {
			copy(s.active[i:], s.active[i+1:])
			s.active[len(s.active)-1] = nil
			s.active = s.active[:len(s.active)-1]
			break
		}
	}
	if !s.closed {
		s.appendAuditLocked(request, reason)
	}
	if request.auditRelease != nil {
		request.auditRelease()
		request.auditRelease = nil
	}
	request.release()
}

type contentOwnership struct {
	mu      sync.Mutex
	refs    int
	release func()
}

func (h *contentOwnership) add() { h.mu.Lock(); h.refs++; h.mu.Unlock() }
func (h *contentOwnership) drop() {
	h.mu.Lock()
	h.refs--
	if h.refs == 0 {
		h.release()
	}
	h.mu.Unlock()
}

func (s *contentSubscription[E]) decodeHeader(value *ethapi.Header) (*types.Header, *contentOwnership, error) {
	// Header strings are already admitted; reserve their serialization and
	// decoder workspace before producing another private representation.
	bytes := uint64(unsafe.Sizeof(*value)) + 8192 + uint64(len(value.ExtraData))
	release, err := s.budget.Reserve(bytes * 256)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	head, err := objectheader.Decode(raw)
	if err != nil {
		return nil, nil, err
	}
	retained, err := s.budget.Reserve(bytes)
	if err != nil {
		return nil, nil, err
	}
	return head, &contentOwnership{refs: 1, release: retained}, nil
}

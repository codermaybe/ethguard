package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	objectheader "github.com/codermaybe/ethguard/internal/ethobject/header"
	sub "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// LogsSubscriptionPolicy separates Head topology memory from log content
// memory. Both limits and content concurrency/response limits must be positive.
// MaxResponseBytes is an application admission limit; transport still owns its
// independent wire-body limit. Each active content supplier reserves a bounded
// decode workspace before its calls. Zero durations disable their deadlines;
// RetryDelay zero disables retries, MaxRecoveryAttempts zero disables its cap.
// A single request slot with no timeout can wait indefinitely for that source.
type LogsSubscriptionPolicy struct {
	Head                                        HeadSubscriptionPolicy
	MemoryBytes, MaxResponseBytes               uint64
	MaxConcurrentRequests                       int
	RequestTimeout, RecoveryTimeout, RetryDelay time.Duration
	MaxRecoveryAttempts                         uint64
}

type LogsContentReason uint8

const (
	LogsRequestFailure LogsContentReason = iota
	LogsInvalidContent
	LogsRequestTimeout
	LogsResponseTooLarge
	LogsStaleSession
	LogsRequestCanceled
	LogsRequestUnfinished
	LogsContentVerified
)

type LogsProofKind uint8

const (
	LogsFilterExcluded LogsProofKind = iota
	LogsBloomExcluded
	LogsBlockCommitment
)

// LogsContentAudit records bounded supplier outcomes; it never retains raw
// provider payloads or arbitrary error strings.
type LogsContentAudit struct {
	Sequence, Attempt, RequestID uint64
	Session                      sub.SourceSession
	Block                        AnchorNode
	Reason                       LogsContentReason
	At                           int64
}

type LogsDelivery struct {
	Node AnchorNode
	Logs []ethapi.Log
	// ContentSource is empty when the filter or committed header bloom proves
	// the empty result without fetching block content.
	ContentSource  string
	ObjectSupplier HeadObjectSupplier
	Proof          LogsProofKind
}

type LogsPathDelta struct {
	BaseVersion, NewVersion uint64
	CommonAncestor          AnchorNode
	// Retracted removes every previously delivered log for each listed block.
	Retracted     []AnchorNode
	Appended      []LogsDelivery
	PreBirthProof []AnchorNode
	Reason        HeadPathChangeReason
}

type LogsEvent struct {
	Path         LogsPathDelta
	Evaluation   HeadEvaluation
	Audit        HeadAuditRange
	ContentAudit []LogsContentAudit
}

type LogsTerminalError struct {
	Cause        error
	CommittedTip AnchorNode
	PathVersion  uint64
	// Audit contains the consumed Head delta or Head producer terminal that
	// could not become a complete Logs event.
	Audit        HeadAuditRange
	ContentAudit []LogsContentAudit
}

func (e *LogsTerminalError) Error() string {
	return fmt.Sprintf("logs subscription terminated: %v", e.Cause)
}
func (e *LogsTerminalError) Unwrap() error { return e.Cause }

type queuedLogsEvent struct {
	event    LogsEvent
	releases []func()
	next     *queuedLogsEvent
}

// LogsSubscription owns one private Head stream and consumes every committed
// path delta in FIFO order. It retains only pending/queued work, never a second
// DAG or historical log table. Next transfers a complete event to its caller;
// Close discards pending delivery, cancels work and joins all suppliers.
type LogsSubscription struct {
	life                              context.Context
	cancel                            context.CancelFunc
	done                              chan struct{}
	drained                           chan struct{}
	drainedOnce                       sync.Once
	head                              *HeadSubscription
	budget                            *sub.AtomicMemoryBudget
	policy                            LogsSubscriptionPolicy
	filter                            logsFilter
	sources                           []identifiedSubscriptionSource
	slots                             chan struct{}
	wg                                sync.WaitGroup
	mu                                sync.Mutex
	nextMu                            sync.Mutex
	notify                            chan struct{}
	first, last                       *queuedLogsEvent
	closed, aborted, terminalReturned bool
	terminal                          error
	terminalReleases                  []func()
	terminalReserve                   func()
	committedTip                      AnchorNode
	pathVersion                       uint64
	auditSequence                     uint64
	requestSequence                   uint64
	active                            []*logsContentRequest
	auditFirst, auditLast             *logsAuditEntry
}

type logsAuditEntry struct {
	record  LogsContentAudit
	release func()
	next    *logsAuditEntry
}
type logsContentRequest struct {
	supplier     HeadObjectSupplier
	attempt      uint64
	node         AnchorNode
	auditRelease func()
	release      func()
}

func validateLogsPolicy(p LogsSubscriptionPolicy) error {
	if err := validateHeadPolicy(p.Head); err != nil {
		return err
	}
	if p.MemoryBytes == 0 || p.MaxResponseBytes == 0 || p.MaxResponseBytes > (^uint64(0)-16384)/256 || p.MaxConcurrentRequests <= 0 {
		return fmt.Errorf("%w: positive logs memory, response bound and concurrency required", rpc.ErrInvalidRequest)
	}
	if p.RequestTimeout < 0 || p.RecoveryTimeout < 0 || p.RetryDelay < 0 {
		return fmt.Errorf("%w: negative logs duration", rpc.ErrInvalidRequest)
	}
	return nil
}

func startLogsSubscription(owner, setup context.Context, sources []identifiedSubscriptionSource, filter logsFilter, p LogsSubscriptionPolicy, mode rpc.CorroborationMode) (*LogsSubscription, error) {
	if err := validateLogsPolicy(p); err != nil {
		return nil, err
	}
	if err := setup.Err(); err != nil {
		return nil, err
	}
	budget := sub.NewMemoryBudget(p.MemoryBytes)
	fixed, err := budget.Reserve(uint64(unsafe.Sizeof(LogsSubscription{})) + 16384 + uint64(len(sources))*512 + uint64(p.MaxConcurrentRequests)*512 + logsFilterBytes(filter))
	if err != nil {
		return nil, err
	}
	terminalReserve, err := budget.Reserve(uint64(unsafe.Sizeof(LogsTerminalError{})) + 256)
	if err != nil {
		fixed()
		return nil, err
	}
	life, cancel := context.WithCancel(owner)
	head, err := startNewHeadSubscription(life, setup, sources, p.Head, mode)
	if err != nil {
		cancel()
		fixed()
		terminalReserve()
		return nil, err
	}
	s := &LogsSubscription{life: life, cancel: cancel, done: make(chan struct{}), drained: make(chan struct{}), head: head, budget: budget, policy: p, filter: filter, sources: append([]identifiedSubscriptionSource(nil), sources...), slots: make(chan struct{}, p.MaxConcurrentRequests), notify: make(chan struct{}, 1), terminalReserve: terminalReserve}
	go s.run(fixed)
	go func() {
		select {
		case <-owner.Done():
			s.abort()
		case <-s.drained:
		}
	}()
	return s, nil
}

// Basis describes the companion Head path domain. Content is supplied by one
// hash-bound proof and does not acquire a separate quorum claim.
func (s *LogsSubscription) Basis() HeadEvidenceBasis { return s.head.Basis() }
func (s *LogsSubscription) CommittedTip() (AnchorNode, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committedTip, s.pathVersion
}

func releaseLogsOwnership(releases []func()) {
	for _, release := range releases {
		if release != nil {
			release()
		}
	}
}
func (s *LogsSubscription) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *LogsSubscription) abort() {
	s.cancel()
	s.mu.Lock()
	s.aborted, s.closed = true, true
	for s.first != nil {
		item := s.first
		s.first = item.next
		releaseLogsOwnership(item.releases)
		item.next = nil
	}
	s.last = nil
	releaseLogsOwnership(s.terminalReleases)
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
func (s *LogsSubscription) Close() error { s.abort(); <-s.done; return nil }

func (s *LogsSubscription) Next(ctx context.Context) (LogsEvent, error) {
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			_ = s.Close()
			return LogsEvent{}, err
		}
		s.mu.Lock()
		if s.first != nil {
			item := s.first
			s.first = item.next
			if s.first == nil {
				s.last = nil
			}
			s.mu.Unlock()
			releaseLogsOwnership(item.releases)
			return item.event, nil
		}
		if s.closed {
			if s.aborted {
				s.mu.Unlock()
				return LogsEvent{}, sub.ErrClosed
			}
			if s.terminalReturned {
				s.mu.Unlock()
				return LogsEvent{}, sub.ErrNextAfterTerminal
			}
			s.terminalReturned = true
			s.drainedOnce.Do(func() { close(s.drained) })
			err, releases := s.terminal, s.terminalReleases
			s.terminal, s.terminalReleases = nil, nil
			s.mu.Unlock()
			releaseLogsOwnership(releases)
			return LogsEvent{}, err
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			_ = s.Close()
			return LogsEvent{}, ctx.Err()
		case <-s.notify:
		}
	}
}

func (s *LogsSubscription) publish(e LogsEvent, releases []func()) error {
	reserved, err := s.budget.Reserve(uint64(unsafe.Sizeof(queuedLogsEvent{})) + uint64(cap(e.Path.Appended))*uint64(unsafe.Sizeof(LogsDelivery{})) + uint64(cap(releases)+1)*8)
	if err != nil {
		return err
	}
	releases = append(releases, reserved)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		reserved()
		return sub.ErrClosed
	}
	if e.Path.BaseVersion != s.pathVersion {
		s.mu.Unlock()
		reserved()
		return sub.ErrStalePathVersion
	}
	e.ContentAudit, releases = s.takeAuditLocked(releases)
	s.pathVersion = e.Path.NewVersion
	s.committedTip = e.Path.CommonAncestor
	if len(e.Path.Appended) > 0 {
		s.committedTip = e.Path.Appended[len(e.Path.Appended)-1].Node
	}
	item := &queuedLogsEvent{event: e, releases: releases}
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

func (s *LogsSubscription) finish(err error, audit HeadAuditRange, content []LogsContentAudit, releases []func()) {
	s.cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		releaseLogsOwnership(releases)
		return
	}
	s.closed = true
	// This lock is the terminal audit cut. In-flight work is identified as
	// unfinished now; later completions only release ownership. Close remains
	// the join barrier, so an uncooperative supplier cannot hide this error.
	for _, request := range s.active {
		s.appendAuditLocked(request, LogsRequestUnfinished)
	}
	pending, releases := s.takeAuditLocked(releases)
	content = append(content, pending...)
	s.terminal = &LogsTerminalError{Cause: err, CommittedTip: s.committedTip, PathVersion: s.pathVersion, Audit: audit, ContentAudit: content}
	s.terminalReleases = append(releases, s.terminalReserve)
	s.terminalReserve = nil
	s.mu.Unlock()
	s.signal()
}

func (s *LogsSubscription) appendAuditLocked(request *logsContentRequest, reason LogsContentReason) {
	if request.auditRelease == nil {
		return
	}
	s.auditSequence++
	entry := &logsAuditEntry{record: LogsContentAudit{Sequence: s.auditSequence, Attempt: request.attempt, RequestID: request.supplier.RequestID, Session: request.supplier.Session, Block: request.node, Reason: reason, At: time.Now().UnixNano()}, release: request.auditRelease}
	request.auditRelease = nil
	if s.auditLast == nil {
		s.auditFirst = entry
	} else {
		s.auditLast.next = entry
	}
	s.auditLast = entry
}
func (s *LogsSubscription) takeAuditLocked(releases []func()) ([]LogsContentAudit, []func()) {
	var out []LogsContentAudit
	for entry := s.auditFirst; entry != nil; entry = entry.next {
		out = append(out, entry.record)
		releases = append(releases, entry.release)
	}
	s.auditFirst, s.auditLast = nil, nil
	return out, releases
}
func (s *LogsSubscription) dropAuditLocked() {
	for entry := s.auditFirst; entry != nil; entry = entry.next {
		entry.release()
	}
	s.auditFirst, s.auditLast = nil, nil
}

func (s *LogsSubscription) beginRequest(source identifiedSubscriptionSource, node AnchorNode, attempt uint64) (*logsContentRequest, error) {
	release, err := s.budget.Reserve(uint64(unsafe.Sizeof(logsContentRequest{})) + 256)
	if err != nil {
		return nil, err
	}
	auditRelease, err := s.budget.Reserve(uint64(unsafe.Sizeof(logsAuditEntry{})) + 2*uint64(unsafe.Sizeof(LogsContentAudit{})) + 128)
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
		return nil, sub.ErrClosed
	}
	s.requestSequence++
	request := &logsContentRequest{supplier: HeadObjectSupplier{Session: session, Acquisition: HeadByHash, RequestID: s.requestSequence, TargetHash: node.Hash}, attempt: attempt, node: node, release: release, auditRelease: auditRelease}
	s.active = append(s.active, request)
	return request, nil
}

func (s *LogsSubscription) completeRequest(request *logsContentRequest, result *logsFetchResult) {
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
		s.appendAuditLocked(request, result.reason)
	}
	if request.auditRelease != nil {
		request.auditRelease()
		request.auditRelease = nil
	}
	request.release()
}

func (s *LogsSubscription) run(fixed func()) {
	defer func() {
		s.cancel()
		_ = s.head.Close()
		s.wg.Wait()
		s.sources = nil
		s.filter = logsFilter{}
		s.active = nil
		fixed()
		close(s.done)
	}()
	for {
		e, hold, err := s.head.queue.NextWithBudget(s.life, s.budget)
		if err != nil {
			if s.life.Err() != nil {
				if hold != nil {
					hold()
				}
				s.abort()
				return
			}
			var audit HeadAuditRange
			var terminal *sub.TerminalError
			if errors.As(err, &terminal) {
				audit = terminal.Audit
			}
			var transfer *sub.TransferError
			if errors.As(err, &transfer) {
				audit = transfer.Audit
			}
			s.finish(err, audit, nil, []func(){hold})
			return
		}
		releases := []func(){hold}
		if e.Delta == nil {
			s.finish(fmt.Errorf("ethguard: missing logs Head delta"), e.Audit, nil, releases)
			return
		}
		d := e.Delta
		deltaHold, reserveErr := s.budget.Reserve(uint64(len(d.Appended))*uint64(unsafe.Sizeof(LogsDelivery{})) + 256)
		if reserveErr != nil {
			s.finish(reserveErr, e.Audit, nil, releases)
			return
		}
		releases = append(releases, deltaHold)
		out := LogsEvent{Path: LogsPathDelta{BaseVersion: d.BaseVersion, NewVersion: d.NewVersion, CommonAncestor: d.CommonAncestor, Retracted: d.Retracted, PreBirthProof: d.PreBirthProof, Reason: d.Reason, Appended: make([]LogsDelivery, 0, len(d.Appended))}, Evaluation: e.Evaluation, Audit: e.Audit}
		for _, entry := range d.Appended {
			value, ok := entry.HeaderValue.(*ethapi.Header)
			if !ok {
				err = fmt.Errorf("ethguard: missing logs header")
				break
			}
			head, headerOwnership, decodeErr := s.decodeHeader(value)
			if decodeErr != nil {
				err = decodeErr
				break
			}
			delivery := LogsDelivery{Node: entry.Node, Logs: make([]ethapi.Log, 0), Proof: LogsFilterExcluded}
			if s.filter.includes(entry.Node) {
				delivery.Proof = LogsBloomExcluded
			}
			if s.filter.includes(entry.Node) && s.filter.mayMatch(head.Bloom) {
				var held []func()
				delivery.Logs, delivery.ObjectSupplier, held, err = s.fetchBlock(head, headerOwnership, entry.Node)
				delivery.ContentSource = delivery.ObjectSupplier.Session.SourceID
				delivery.Proof = LogsBlockCommitment
				releases = append(releases, held...)
				if err != nil {
					headerOwnership.drop()
					break
				}
			}
			headerOwnership.drop()
			out.Path.Appended = append(out.Path.Appended, delivery)
		}
		if err != nil {
			s.finish(err, out.Audit, out.ContentAudit, releases)
			return
		}
		if err = s.publish(out, releases); err != nil {
			s.finish(err, out.Audit, out.ContentAudit, releases)
			return
		}
	}
}

type logsFetchResult struct {
	logs     []ethapi.Log
	source   string
	err      error
	reason   LogsContentReason
	release  func()
	supplier HeadObjectSupplier
}

func (s *LogsSubscription) fetchBlock(head *types.Header, headerOwnership *logsHeaderOwnership, node AnchorNode) ([]ethapi.Log, HeadObjectSupplier, []func(), error) {
	ctx, cancel := context.WithCancel(s.life)
	if s.policy.RecoveryTimeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(s.life, s.policy.RecoveryTimeout)
	}
	defer cancel()
	var holds []func()
	for attempt := uint64(1); ; attempt++ {
		group, stop := context.WithCancel(ctx)
		results := make(chan logsFetchResult)
		next, active := 0, 0
		for next < len(s.sources) || active > 0 {
			var slot chan struct{}
			if next < len(s.sources) {
				slot = s.slots
			}
			select {
			case slot <- struct{}{}:
				source := s.sources[next]
				request, err := s.beginRequest(source, node, attempt)
				if err != nil {
					<-s.slots
					stop()
					return nil, HeadObjectSupplier{}, holds, err
				}
				next++
				active++
				s.wg.Add(1)
				headerOwnership.add()
				go func() {
					defer s.wg.Done()
					defer headerOwnership.drop()
					defer func() { <-s.slots }()
					result := s.fetchSupplier(group, source, head, request.supplier)
					s.completeRequest(request, &result)
					select {
					case results <- result:
					case <-group.Done():
						if result.release != nil {
							result.release()
						}
					}
				}()
			case result := <-results:
				active--
				if err := ctx.Err(); err != nil {
					if result.release != nil {
						result.release()
					}
					stop()
					return nil, HeadObjectSupplier{}, holds, err
				}
				if result.err == nil {
					stop()
					holds = append(holds, result.release)
					return result.logs, result.supplier, holds, nil
				}
				if errors.Is(result.err, sub.ErrMemoryBudget) {
					stop()
					return nil, HeadObjectSupplier{}, holds, result.err
				}
			case <-ctx.Done():
				stop()
				return nil, HeadObjectSupplier{}, holds, ctx.Err()
			}
		}
		stop()
		if s.policy.RetryDelay == 0 || (s.policy.MaxRecoveryAttempts > 0 && attempt >= s.policy.MaxRecoveryAttempts) {
			return nil, HeadObjectSupplier{}, holds, sub.ErrRecoveryExhausted
		}
		if !waitSubscriptionRetry(ctx.Done(), ctx, s.policy.RetryDelay) {
			return nil, HeadObjectSupplier{}, holds, ctx.Err()
		}
	}
}

func (s *LogsSubscription) fetchSupplier(ctx context.Context, source identifiedSubscriptionSource, head *types.Header, supplier HeadObjectSupplier) (out logsFetchResult) {
	out.source = source.ID()
	out.supplier = supplier
	if err := s.head.sessions.Accept(supplier.Session); err != nil {
		out.err = err
		out.reason = LogsStaleSession
		return
	}
	defer func() {
		if err := s.head.sessions.Accept(supplier.Session); err != nil && (out.err == nil || out.reason != LogsInvalidContent) {
			out.err = err
			out.reason = LogsStaleSession
		}
		if out.err != nil {
			if out.release != nil {
				out.release()
				out.release = nil
			}
			out.logs = nil
		}
	}()
	out.reason = LogsRequestFailure
	unary, ok := source.(subscriptionUnarySource)
	if !ok {
		out.err = fmt.Errorf("ethguard: logs supplier has no unary calls")
		return
	}
	workspace, err := s.budget.Reserve(s.policy.MaxResponseBytes*256 + 16384)
	if err != nil {
		out.err = err
		return
	}
	defer workspace()
	if s.policy.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.policy.RequestTimeout)
		defer cancel()
	}
	defer func() {
		if err := ctx.Err(); err != nil && out.err == nil {
			out.err = err
		}
		if errors.Is(out.err, context.DeadlineExceeded) {
			out.reason = LogsRequestTimeout
		} else if errors.Is(out.err, context.Canceled) {
			out.reason = LogsRequestCanceled
		}
	}()
	call := func(ctx context.Context, method string, args []any) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := unary.CallContext(ctx, method, args...)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if err != nil {
			return nil, err
		}
		if uint64(len(raw)) > s.policy.MaxResponseBytes {
			out.reason = LogsResponseTooLarge
			return nil, transport.ErrBodyTooLarge
		}
		return raw, nil
	}
	raw, err := call(ctx, "eth_getBlockByHash", []any{head.Hash().Hex(), true})
	if err != nil {
		out.err = err
		return
	}
	out.reason = LogsInvalidContent
	txs, err := verifyLogsBlock(ctx, head, raw, call)
	if err != nil {
		out.err = err
		return
	}
	out.reason = LogsRequestFailure
	raw, err = call(ctx, "eth_getBlockReceipts", []any{head.Hash().Hex()})
	if err != nil {
		out.err = err
		return
	}
	out.reason = LogsInvalidContent
	all, err := proveBlockLogs(head, txs, raw)
	if err != nil {
		out.err = err
		return
	}
	matched := make([]ethapi.Log, 0, len(all))
	for _, entry := range all {
		if s.filter.matches(entry) {
			matched = append(matched, entry)
		}
	}
	// Copy the selected slice so unmatched entries do not remain retained.
	out.logs = append(make([]ethapi.Log, 0, len(matched)), matched...)
	out.release, out.err = s.budget.Reserve(logsContentBytes(out.logs) + uint64(len(out.source)))
	if out.err == nil {
		out.reason = LogsContentVerified
	}
	return
}

type logsHeaderOwnership struct {
	mu      sync.Mutex
	refs    int
	release func()
}

func (h *logsHeaderOwnership) add() { h.mu.Lock(); h.refs++; h.mu.Unlock() }
func (h *logsHeaderOwnership) drop() {
	h.mu.Lock()
	h.refs--
	if h.refs == 0 {
		h.release()
	}
	h.mu.Unlock()
}

func (s *LogsSubscription) decodeHeader(value *ethapi.Header) (*types.Header, *logsHeaderOwnership, error) {
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
	return head, &logsHeaderOwnership{refs: 1, release: retained}, nil
}

func logsContentBytes(logs []ethapi.Log) uint64 {
	n := uint64(cap(logs)) * uint64(unsafe.Sizeof(ethapi.Log{}))
	for _, entry := range logs {
		n += 256 + uint64(len(*entry.Address)+len(*entry.Data)+len(entry.TransactionHash)+len(*entry.BlockHash)+len(*entry.BlockNumber)+len(*entry.BlockTimestamp)+len(*entry.TransactionIndex)+len(*entry.LogIndex)) + uint64(cap(*entry.Topics))*uint64(unsafe.Sizeof(ethapi.Hash("")))
		for _, topic := range *entry.Topics {
			n += uint64(len(topic))
		}
	}
	return n
}

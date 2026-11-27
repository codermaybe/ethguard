package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
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

type LogsProofKind uint8

const (
	LogsFilterExcluded LogsProofKind = iota
	LogsBloomExcluded
	LogsBlockCommitment
)

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
	ContentAudit []ContentAudit
}

type LogsTerminalError struct {
	Cause        error
	CommittedTip AnchorNode
	PathVersion  uint64
	// Audit contains the consumed Head delta or Head producer terminal that
	// could not become a complete Logs event.
	Audit        HeadAuditRange
	ContentAudit []ContentAudit
}

func (e *LogsTerminalError) Error() string {
	return fmt.Sprintf("logs subscription terminated: %v", e.Cause)
}
func (e *LogsTerminalError) Unwrap() error { return e.Cause }

type LogsSubscription struct {
	*contentSubscription[LogsEvent]
	policy LogsSubscriptionPolicy
	filter logsFilter
}

// NewLogs freezes the filter and validates its policy before obtaining sources.
func NewLogs(owner, setup context.Context, load func() ([]Source, error), q ethapi.FilterQuery, p LogsSubscriptionPolicy, mode rpc.CorroborationMode) (*LogsSubscription, error) {
	filter, err := compileLogsFilter(q)
	if err != nil {
		return nil, err
	}
	if err := validateLogsPolicy(p); err != nil {
		return nil, err
	}
	sources, err := snapshotSources(load, mode)
	if err != nil {
		return nil, err
	}
	shared, err := newContentSubscription[LogsEvent](owner, setup, sources, p.Head, p.MemoryBytes, p.MaxConcurrentRequests, uint64(unsafe.Sizeof(LogsSubscription{}))+logsFilterBytes(filter), mode)
	if err != nil {
		return nil, err
	}
	s := &LogsSubscription{contentSubscription: shared, policy: p, filter: filter}
	go s.run()
	return s, nil
}

// Basis describes the companion Head path; log content is hash-bound proof.
func (s *LogsSubscription) Next(ctx context.Context) (LogsEvent, error) {
	event, audit, err := s.next(ctx)
	if err != nil {
		var terminal *contentTerminalError
		if errors.As(err, &terminal) {
			return LogsEvent{}, &LogsTerminalError{Cause: terminal.Cause, CommittedTip: terminal.CommittedTip, PathVersion: terminal.PathVersion, Audit: terminal.Audit, ContentAudit: terminal.ContentAudit}
		}
		return LogsEvent{}, err
	}
	event.ContentAudit = audit
	return event, nil
}
func (s *LogsSubscription) publish(event LogsEvent, releases []func()) error {
	tip := event.Path.CommonAncestor
	if n := len(event.Path.Appended); n != 0 {
		tip = event.Path.Appended[n-1].Node
	}
	return s.contentSubscription.publish(event, event.Path.BaseVersion, event.Path.NewVersion, tip, uint64(cap(event.Path.Appended))*uint64(unsafe.Sizeof(LogsDelivery{})), releases)
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

func (s *LogsSubscription) run() {
	defer func() { s.joinWork(); s.filter = logsFilter{}; close(s.done) }()
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
			var terminal *TerminalError
			if errors.As(err, &terminal) {
				audit = terminal.Audit
			}
			var transfer *TransferError
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
	reason   ContentReason
	release  func()
	supplier HeadObjectSupplier
}

func (s *LogsSubscription) fetchBlock(head *types.Header, headerOwnership *contentOwnership, node AnchorNode) ([]ethapi.Log, HeadObjectSupplier, []func(), error) {
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
				request, err := s.beginRequest(source, node, attempt, ContentLogProof)
				if err != nil {
					<-s.slots
					stop()
					return nil, HeadObjectSupplier{}, holds, err
				}
				next++
				active++
				headerOwnership.add()
				go func() {
					defer s.wg.Done()
					defer headerOwnership.drop()
					defer func() { <-s.slots }()
					result := s.fetchSupplier(group, source, head, request.supplier)
					s.completeRequest(request, result.reason)
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
				if errors.Is(result.err, ErrMemoryBudget) {
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
			return nil, HeadObjectSupplier{}, holds, ErrRecoveryExhausted
		}
		if !waitSubscriptionRetry(ctx.Done(), ctx, s.policy.RetryDelay) {
			return nil, HeadObjectSupplier{}, holds, ctx.Err()
		}
	}
}

func (s *LogsSubscription) fetchSupplier(ctx context.Context, source Source, head *types.Header, supplier HeadObjectSupplier) (out logsFetchResult) {
	out.source = source.ID()
	out.supplier = supplier
	if err := s.head.sessions.Accept(supplier.Session); err != nil {
		out.err = err
		out.reason = ContentStaleSession
		return
	}
	defer func() {
		if err := s.head.sessions.Accept(supplier.Session); err != nil && (out.err == nil || out.reason != ContentInvalid) {
			out.err = err
			out.reason = ContentStaleSession
		}
		if out.err != nil {
			if out.release != nil {
				out.release()
				out.release = nil
			}
			out.logs = nil
		}
	}()
	out.reason = ContentRequestFailure
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
			out.reason = ContentRequestTimeout
		} else if errors.Is(out.err, context.Canceled) {
			out.reason = ContentRequestCanceled
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
			out.reason = ContentResponseTooLarge
			return nil, transport.ErrBodyTooLarge
		}
		return raw, nil
	}
	raw, err := call(ctx, "eth_getBlockByHash", []any{head.Hash().Hex(), true})
	if err != nil {
		out.err = err
		return
	}
	out.reason = ContentInvalid
	txs, err := verifyBlockTransactions(ctx, head, raw, call)
	if err != nil {
		out.err = err
		return
	}
	out.reason = ContentRequestFailure
	raw, err = call(ctx, "eth_getBlockReceipts", []any{head.Hash().Hex()})
	if err != nil {
		out.err = err
		return
	}
	out.reason = ContentInvalid
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
		out.reason = ContentVerified
	}
	return
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

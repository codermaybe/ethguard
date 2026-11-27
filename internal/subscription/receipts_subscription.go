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
	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// ReceiptsSubscriptionPolicy independently bounds Head topology and receipt
// content. Zero durations disable deadlines; zero RetryDelay disables retries.
// The response bound reserves decode workspace before each supplier starts.
type ReceiptsSubscriptionPolicy struct {
	Head                                        HeadSubscriptionPolicy
	MemoryBytes, MaxResponseBytes               uint64
	MaxConcurrentRequests                       int
	RequestTimeout, RecoveryTimeout, RetryDelay time.Duration
	MaxRecoveryAttempts                         uint64
}

type ReceiptsProofKind uint8

const (
	ReceiptsTransactionsExcluded ReceiptsProofKind = iota
	ReceiptsEmptyBlock
	ReceiptsBlockCommitment
)

// ReceiptSupport describes the official values declared by distinct members
// in one attempt. It does not claim chain execution or fork-rule validation.
type ReceiptSupport struct {
	Status          rpc.Status
	Basis, Required int
	Attempt         uint64
	Representative  ObjectSupplier
	Suppliers       []ObjectSupplier
}

type ReceiptsDelivery struct {
	Node           AnchorNode
	Receipts       []ethapi.Receipt
	BlockSupplier  ObjectSupplier
	ReceiptSupport ReceiptSupport
	Proof          ReceiptsProofKind
}

type ReceiptsPathDelta struct {
	BaseVersion, NewVersion uint64
	CommonAncestor          AnchorNode
	Retracted               []AnchorNode
	Appended                []ReceiptsDelivery
	PreBirthProof           []AnchorNode
	Reason                  PathChangeReason
}

type ReceiptsEvent struct {
	Path         ReceiptsPathDelta
	Evaluation   EvaluationSnapshot
	Audit        AuditRange
	ContentAudit []ContentAudit
}

type ReceiptsTerminalError struct {
	Cause        error
	CommittedTip AnchorNode
	PathVersion  uint64
	Audit        AuditRange
	ContentAudit []ContentAudit
}

func (e *ReceiptsTerminalError) Error() string {
	return fmt.Sprintf("receipts subscription terminated: %v", e.Cause)
}
func (e *ReceiptsTerminalError) Unwrap() error { return e.Cause }

// ReceiptsSubscription follows its private Head's complete path from birth.
// It commits only complete content deltas; Next transfers caller ownership,
// and Close aborts unread delivery and joins all actual work.
type ReceiptsSubscription struct {
	*contentSubscription[ReceiptsEvent]
	policy   ReceiptsSubscriptionPolicy
	filter   receiptsFilter
	rule     rpcengine.Corroboration
	resolver rpcengine.CanonicalOutcomeResolver
}

type subscriptionResponseSource interface {
	Call(context.Context, string, ...any) rpc.Response
}

func validateReceiptsPolicy(p ReceiptsSubscriptionPolicy) error {
	if err := validateHeadPolicy(p.Head); err != nil {
		return err
	}
	if p.MemoryBytes == 0 || p.MaxResponseBytes == 0 || p.MaxResponseBytes > (^uint64(0)-16384)/256 || p.MaxConcurrentRequests <= 0 {
		return fmt.Errorf("%w: positive receipts memory, response bound and concurrency required", rpc.ErrInvalidRequest)
	}
	if p.RequestTimeout < 0 || p.RecoveryTimeout < 0 || p.RetryDelay < 0 {
		return fmt.Errorf("%w: negative receipts duration", rpc.ErrInvalidRequest)
	}
	return nil
}

// NewReceipts freezes the filter and validates its policy before obtaining sources.
func NewReceipts(owner, setup context.Context, load func() ([]Source, error), q *ethapi.TransactionReceiptsQuery, p ReceiptsSubscriptionPolicy, mode rpc.CorroborationMode) (*ReceiptsSubscription, error) {
	filter, err := compileReceiptsFilter(q)
	if err != nil {
		return nil, err
	}
	if err := validateReceiptsPolicy(p); err != nil {
		return nil, err
	}
	sources, err := snapshotSources(load, mode)
	if err != nil {
		return nil, err
	}
	resolver, ok := rpcengine.Strict(rpc.CanonicalJSON).(rpcengine.CanonicalOutcomeResolver)
	if !ok {
		return nil, rpc.ErrInvalidCanonicalResult
	}
	shared, err := newContentSubscription[ReceiptsEvent](owner, setup, sources, p.Head, p.MemoryBytes, p.MaxConcurrentRequests, uint64(unsafe.Sizeof(ReceiptsSubscription{}))+uint64(len(filter))*192, mode)
	if err != nil {
		return nil, err
	}
	basis := shared.Basis()
	rule := rpcengine.Corroboration{Mode: rpc.CorroborationMajority, Floor: basis.Quorum}
	if basis.Tier == Forwarded {
		rule = rpcengine.Corroboration{Mode: rpc.CorroborationForwarded}
	}
	s := &ReceiptsSubscription{contentSubscription: shared, policy: p, filter: filter, rule: rule, resolver: resolver}
	go s.run()
	return s, nil
}

func (s *ReceiptsSubscription) Next(ctx context.Context) (ReceiptsEvent, error) {
	event, audit, err := s.next(ctx)
	if err != nil {
		var terminal *contentTerminalError
		if errors.As(err, &terminal) {
			return ReceiptsEvent{}, &ReceiptsTerminalError{Cause: terminal.Cause, CommittedTip: terminal.CommittedTip, PathVersion: terminal.PathVersion, Audit: terminal.Audit, ContentAudit: terminal.ContentAudit}
		}
		return ReceiptsEvent{}, err
	}
	event.ContentAudit = audit
	return event, nil
}

func (s *ReceiptsSubscription) run() {
	defer func() { s.joinWork(); s.filter = nil; close(s.done) }()
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
			var audit AuditRange
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
			s.finish(errors.New("ethguard: missing receipts Head delta"), e.Audit, nil, releases)
			return
		}
		d := e.Delta
		reservation, reserveErr := s.budget.Reserve(uint64(len(d.Appended))*uint64(unsafe.Sizeof(ReceiptsDelivery{})) + 256)
		if reserveErr != nil {
			s.finish(reserveErr, e.Audit, nil, releases)
			return
		}
		releases = append(releases, reservation)
		out := ReceiptsEvent{Path: ReceiptsPathDelta{BaseVersion: d.BaseVersion, NewVersion: d.NewVersion, CommonAncestor: d.CommonAncestor, Retracted: d.Retracted, PreBirthProof: d.PreBirthProof, Reason: d.Reason, Appended: make([]ReceiptsDelivery, 0, len(d.Appended))}, Evaluation: e.Evaluation, Audit: e.Audit}
		for _, entry := range d.Appended {
			value, ok := entry.HeaderValue.(*ethapi.Header)
			if !ok {
				err = errors.New("ethguard: missing receipts header")
				break
			}
			head, ownership, decodeErr := s.decodeHeader(value)
			if decodeErr != nil {
				err = decodeErr
				break
			}
			delivery, holds, fetchErr := s.fetchBlock(head, ownership, entry.Node)
			ownership.drop()
			releases = append(releases, holds...)
			if fetchErr != nil {
				err = fetchErr
				break
			}
			out.Path.Appended = append(out.Path.Appended, delivery)
		}
		if err != nil {
			s.finish(err, out.Audit, nil, releases)
			return
		}
		tip := out.Path.CommonAncestor
		if n := len(out.Path.Appended); n != 0 {
			tip = out.Path.Appended[n-1].Node
		}
		if err = s.publish(out, out.Path.BaseVersion, out.Path.NewVersion, tip, uint64(cap(out.Path.Appended))*uint64(unsafe.Sizeof(ReceiptsDelivery{})), releases); err != nil {
			s.finish(err, out.Audit, nil, releases)
			return
		}
	}
}

func (s *ReceiptsSubscription) fetchBlock(head *types.Header, headerHold *contentOwnership, node AnchorNode) (ReceiptsDelivery, []func(), error) {
	ctx, cancel := context.WithCancel(s.life)
	if s.policy.RecoveryTimeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(s.life, s.policy.RecoveryTimeout)
	}
	defer cancel()
	txs, supplier, txHold, err := s.fetchTransactions(ctx, head, headerHold, node)
	if err != nil {
		return ReceiptsDelivery{}, nil, err
	}
	defer txHold.drop()
	indices := s.filter.indices(txs)
	delivery := ReceiptsDelivery{Node: node, Receipts: make([]ethapi.Receipt, 0), BlockSupplier: supplier, Proof: ReceiptsTransactionsExcluded}
	if len(txs) == 0 {
		if _, err := objectreceipt.VerifyBlockCommitment(head, txs, json.RawMessage(`[]`)); err != nil {
			return ReceiptsDelivery{}, nil, err
		}
		delivery.Proof = ReceiptsEmptyBlock
		return delivery, nil, nil
	}
	if len(indices) == 0 {
		return delivery, nil, nil
	}
	values, support, hold, err := s.fetchReceipts(ctx, head, headerHold, txs, txHold, indices, node)
	if err != nil {
		return ReceiptsDelivery{}, nil, err
	}
	delivery.Receipts, delivery.ReceiptSupport, delivery.Proof = values, support, ReceiptsBlockCommitment
	return delivery, []func(){hold}, nil
}

type transactionContentResult struct {
	txs      types.Transactions
	supplier ObjectSupplier
	hold     *contentOwnership
	err      error
	reason   ContentReason
}

func (s *ReceiptsSubscription) fetchTransactions(ctx context.Context, head *types.Header, headerHold *contentOwnership, node AnchorNode) (types.Transactions, ObjectSupplier, *contentOwnership, error) {
	for attempt := uint64(1); ; attempt++ {
		group, stop := context.WithCancel(ctx)
		results := make(chan transactionContentResult)
		next, active := 0, 0
		for next < len(s.sources) || active > 0 {
			var slot chan struct{}
			if next < len(s.sources) {
				slot = s.slots
			}
			select {
			case slot <- struct{}{}:
				source := s.sources[next]
				request, err := s.beginRequest(source, node, attempt, ContentTransactions)
				if err != nil {
					<-s.slots
					stop()
					return nil, ObjectSupplier{}, nil, err
				}
				next++
				active++
				headerHold.add()
				go func() {
					defer s.wg.Done()
					defer headerHold.drop()
					defer func() { <-s.slots }()
					result := s.fetchTransactionSupplier(group, source, head, request.supplier)
					s.completeRequest(request, result.reason)
					select {
					case results <- result:
					case <-group.Done():
						if result.hold != nil {
							result.hold.drop()
						}
					}
				}()
			case result := <-results:
				active--
				if ctx.Err() != nil {
					if result.hold != nil {
						result.hold.drop()
					}
					stop()
					return nil, ObjectSupplier{}, nil, ctx.Err()
				}
				if result.err == nil {
					stop()
					return result.txs, result.supplier, result.hold, nil
				}
				if fatalContentError(result.err) {
					stop()
					return nil, ObjectSupplier{}, nil, result.err
				}
			case <-ctx.Done():
				stop()
				return nil, ObjectSupplier{}, nil, ctx.Err()
			}
		}
		stop()
		if err := s.retry(ctx, attempt); err != nil {
			return nil, ObjectSupplier{}, nil, err
		}
	}
}

func (s *ReceiptsSubscription) retry(ctx context.Context, attempt uint64) error {
	if s.policy.RetryDelay == 0 || (s.policy.MaxRecoveryAttempts > 0 && attempt >= s.policy.MaxRecoveryAttempts) {
		return ErrRecoveryExhausted
	}
	if !waitSubscriptionRetry(ctx.Done(), ctx, s.policy.RetryDelay) {
		return ctx.Err()
	}
	return nil
}

func contentReason(err error, otherwise ContentReason) ContentReason {
	if errors.Is(err, context.DeadlineExceeded) {
		return ContentRequestTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ContentRequestCanceled
	}
	if errors.Is(err, ErrSessionStale) || errors.Is(err, ErrSessionClosed) {
		return ContentStaleSession
	}
	if errors.Is(err, transport.ErrBodyTooLarge) {
		return ContentResponseTooLarge
	}
	return otherwise
}

func fatalContentError(err error) bool {
	return errors.Is(err, ErrMemoryBudget) || errors.Is(err, rpc.ErrInvalidCanonicalResult) || errors.Is(err, rpc.ErrInvalidOutcome)
}

func (s *ReceiptsSubscription) fetchTransactionSupplier(ctx context.Context, source Source, head *types.Header, supplier ObjectSupplier) (out transactionContentResult) {
	out.supplier, out.reason = supplier, ContentRequestFailure
	if err := s.head.sessions.Accept(supplier.Session); err != nil {
		out.err, out.reason = err, ContentStaleSession
		return
	}
	defer func() {
		if err := s.head.sessions.Accept(supplier.Session); err != nil && !fatalContentError(out.err) && (out.err == nil || out.reason != ContentInvalid) {
			out.err = err
			out.reason = ContentStaleSession
		}
		if out.err != nil {
			if out.hold != nil {
				out.hold.drop()
				out.hold = nil
			}
			out.txs = nil
		}
	}()
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
		if ctx.Err() != nil && out.err == nil {
			out.err = ctx.Err()
		}
		out.reason = contentReason(out.err, out.reason)
	}()
	unary, ok := source.(subscriptionResponseSource)
	if !ok {
		out.err = errors.New("ethguard: receipts supplier has no response calls")
		return
	}
	call := func(ctx context.Context, method string, args []any) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response := unary.Call(ctx, method, args...)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := response.Err(); err != nil {
			return nil, err
		}
		if uint64(len(response.Answer.Result)) > s.policy.MaxResponseBytes {
			return nil, transport.ErrBodyTooLarge
		}
		return response.Answer.Result, nil
	}
	raw, err := call(ctx, "eth_getBlockByHash", []any{head.Hash().Hex(), true})
	if err != nil {
		out.err = err
		return
	}
	out.reason = ContentInvalid
	out.txs, out.err = verifyBlockTransactions(ctx, head, raw, call)
	if out.err != nil {
		return
	}
	// The decoded transaction list is immutable and shared with every receipts
	// verifier. Its lease outlives cancellation until all actual workers exit.
	hold, err := s.budget.Reserve(uint64(len(raw))*64 + 16384)
	if err != nil {
		out.err = err
		return
	}
	out.hold = &contentOwnership{refs: 1, release: hold}
	out.reason = ContentVerified
	return
}

var errReceiptNotRequested = errors.New("ethguard: receipt member not requested")
var errReceiptUnfinished = errors.New("ethguard: receipt request unfinished at decision")

type receiptContentResult struct {
	index       int
	observation rpc.Observation
	canonical   rpcengine.CanonicalResult
	supplier    ObjectSupplier
	release     func()
	err         error
	reason      ContentReason
}

func (s *ReceiptsSubscription) fetchReceipts(ctx context.Context, head *types.Header, headerHold *contentOwnership, txs types.Transactions, txHold *contentOwnership, indices []int, node AnchorNode) ([]ethapi.Receipt, ReceiptSupport, func(), error) {
	for attempt := uint64(1); ; attempt++ {
		values, support, hold, err := s.receiptAttempt(ctx, head, headerHold, txs, txHold, indices, node, attempt)
		if err == nil {
			return values, support, hold, nil
		}
		if fatalContentError(err) || ctx.Err() != nil {
			return nil, ReceiptSupport{}, nil, err
		}
		if retryErr := s.retry(ctx, attempt); retryErr != nil {
			return nil, ReceiptSupport{}, nil, errors.Join(retryErr, err)
		}
	}
}

func (s *ReceiptsSubscription) receiptAttempt(ctx context.Context, head *types.Header, headerHold *contentOwnership, txs types.Transactions, txHold *contentOwnership, indices []int, node AnchorNode, attempt uint64) ([]ethapi.Receipt, ReceiptSupport, func(), error) {
	group, stop := context.WithCancel(ctx)
	defer stop()
	// Fixed slots include failures and members never scheduled before Q. Raw
	// answers and canonical/resolver copies remain charged until this attempt ends.
	fixed, err := s.budget.Reserve(uint64(len(s.sources))*2048 + 1024)
	if err != nil {
		return nil, ReceiptSupport{}, nil, err
	}
	defer fixed()
	answers := make([]receiptContentResult, len(s.sources))
	for i, source := range s.sources {
		answers[i].observation = rpc.Observation{Source: rpc.SourceRef{ID: source.ID()}, Response: rpc.Response{Exchange: rpc.Exchange{Cause: errReceiptNotRequested}}}
	}
	defer func() {
		for i := range answers {
			if answers[i].release != nil {
				answers[i].release()
			}
		}
	}()
	results := make(chan receiptContentResult)
	next, active, valid := 0, 0, 0
	var lastErr error = ErrRecoveryExhausted
	for next < len(s.sources) || active > 0 {
		var slot chan struct{}
		if next < len(s.sources) {
			slot = s.slots
		}
		select {
		case slot <- struct{}{}:
			index := next
			source := s.sources[index]
			request, err := s.beginRequest(source, node, attempt, ContentReceipts)
			if err != nil {
				<-s.slots
				return nil, ReceiptSupport{}, nil, err
			}
			next++
			active++
			headerHold.add()
			txHold.add()
			answers[index].observation.Attempted = true
			answers[index].observation.Response.Exchange.Cause = errReceiptUnfinished
			go func() {
				defer s.wg.Done()
				defer headerHold.drop()
				defer txHold.drop()
				defer func() { <-s.slots }()
				result := s.fetchReceiptSupplier(group, source, head, txs, indices, request.supplier)
				result.index = index
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
			answers[result.index] = result
			if ctx.Err() != nil {
				return nil, ReceiptSupport{}, nil, ctx.Err()
			}
			if fatalContentError(result.err) {
				return nil, ReceiptSupport{}, nil, result.err
			}
			if result.err == nil {
				valid++
			}
			if valid < s.rule.Required(len(answers)) {
				continue
			}
			canonical := make([]rpcengine.CanonicalObservation, len(answers))
			for i, answer := range answers {
				canonical[i], err = rpcengine.NewCanonicalObservation(answer.observation, answer.canonical)
				if err != nil {
					return nil, ReceiptSupport{}, nil, err
				}
			}
			outcome, resolveErr := s.resolver.ResolveCanonicalCorroborated(s.rule, canonical)
			if resolveErr != nil {
				if fatalContentError(resolveErr) {
					return nil, ReceiptSupport{}, nil, resolveErr
				}
				lastErr = resolveErr
				continue
			}
			stop()
			support := ReceiptSupport{Status: outcome.Decision.Status, Basis: len(answers), Required: outcome.Decision.Adjudication.Required, Attempt: attempt}
			for _, member := range outcome.Decision.Support {
				for _, answer := range answers {
					if answer.observation.Source.ID == member.ID {
						support.Suppliers = append(support.Suppliers, answer.supplier)
						if member == outcome.Decision.Representative {
							support.Representative = answer.supplier
						}
						break
					}
				}
			}
			// Account for the complete typed result and its independent nested
			// strings/pointers before decoding the winner, not N typed candidates.
			hold, reserveErr := s.budget.Reserve(uint64(len(outcome.Decision.Value))*64 + uint64(len(support.Suppliers))*256 + 4096)
			if reserveErr != nil {
				return nil, ReceiptSupport{}, nil, reserveErr
			}
			values, decodeErr := decodeCommittedReceipts(outcome.Decision.Value)
			if decodeErr != nil {
				hold()
				return nil, ReceiptSupport{}, nil, fmt.Errorf("%w: %v", rpc.ErrInvalidCanonicalResult, decodeErr)
			}
			return values, support, hold, nil
		case <-ctx.Done():
			return nil, ReceiptSupport{}, nil, ctx.Err()
		}
	}
	return nil, ReceiptSupport{}, nil, lastErr
}

func (s *ReceiptsSubscription) fetchReceiptSupplier(ctx context.Context, source Source, head *types.Header, txs types.Transactions, indices []int, supplier ObjectSupplier) (out receiptContentResult) {
	out.supplier, out.reason = supplier, ContentRequestFailure
	out.observation = rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: source.ID()}, Response: rpc.Response{Exchange: rpc.Exchange{Cause: errReceiptNotRequested}}}
	// Local failures before transport keep a truthful not-started response.
	defer func() {
		if err := s.head.sessions.Accept(supplier.Session); err != nil && !fatalContentError(out.err) && (out.err == nil || out.reason != ContentInvalid) {
			out.err, out.reason = err, ContentStaleSession
		}
		if out.err != nil {
			out.canonical = rpcengine.CanonicalResult{}
			if out.observation.Response.Answer.Kind == rpc.AnswerResult && out.observation.Response.Err() == nil {
				state := rpc.InterpretationUnassessed
				if out.reason == ContentInvalid {
					state = rpc.InterpretationRejected
				}
				out.observation.Response.Answer.Interpretation = rpc.Interpretation{State: state, Cause: out.err}
			} else if out.observation.Response.Answer.Kind == rpc.AnswerUnassessed && errors.Is(out.observation.Response.Exchange.Cause, errReceiptNotRequested) {
				out.observation.Response.Exchange.Cause = out.err
			}
		}
	}()
	if err := s.head.sessions.Accept(supplier.Session); err != nil {
		out.err, out.reason = err, ContentStaleSession
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
		if ctx.Err() != nil && out.err == nil {
			out.err = ctx.Err()
		}
		out.reason = contentReason(out.err, out.reason)
	}()
	unary, ok := source.(subscriptionResponseSource)
	if !ok {
		out.err = errors.New("ethguard: receipts supplier has no response calls")
		return
	}
	if err := ctx.Err(); err != nil {
		out.err = err
		return
	}
	out.observation.Response = unary.Call(ctx, "eth_getBlockReceipts", head.Hash().Hex())
	rawBytes := uint64(len(out.observation.Response.Answer.Result))
	if body := out.observation.Response.Answer.RPCError; body != nil {
		rawBytes += uint64(len(body.Message) + len(body.Data))
	}
	// This allowance covers retained raw/canonical candidates and the resolver's
	// bounded copies. Oversized responses remain failures, inside the denominator.
	if rawBytes > (^uint64(0)-4096)/64 {
		out.err = ErrMemoryBudget
		out.observation = rpc.Observation{}
		return
	}
	out.release, out.err = s.budget.Reserve(rawBytes*64 + 4096)
	if out.err != nil {
		// This fatal local failure never reaches the resolver. Drop the borrowed
		// payload before the workspace lease ends; no unowned raw answer crosses
		// the worker handoff, and no substitute wire facts are manufactured.
		out.observation = rpc.Observation{}
		return
	}
	if err := out.observation.Response.Err(); err != nil {
		out.err = err
		return
	}
	if rawBytes > s.policy.MaxResponseBytes {
		out.err = transport.ErrBodyTooLarge
		return
	}
	if err := ctx.Err(); err != nil {
		out.err = err
		return
	}
	out.reason = ContentInvalid
	canonical, err := proveSelectedReceipts(head, txs, indices, out.observation.Response.Answer.Result)
	if err != nil {
		out.err = err
		return
	}
	out.canonical, out.err = rpcengine.NewCanonicalResult(canonical, nil)
	if out.err == nil {
		out.reason = ContentVerified
	}
	return
}

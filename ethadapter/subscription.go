package ethadapter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// SubscriptionAudit retains the native evidence that channel signatures cannot
// express. Phase is setup, event, or terminal. At most one event pointer is set.
// Observers retaining these immutable values own their subsequent memory use.
type SubscriptionAudit struct {
	Phase    string
	Basis    ethguard.HeadEvidenceBasis
	Head     *ethguard.HeadEvent
	Logs     *ethguard.LogsEvent
	Receipts *ethguard.ReceiptsEvent
}

type nativeSubscription[E any] interface {
	NextWithBudget(context.Context, ethguard.SubscriptionMemoryBudget) (E, func(), error)
	Basis() ethguard.HeadEvidenceBasis
	Terminated() <-chan struct{}
	TerminalError() error
	Close() error
}

const bridgeQueueCapacity = 2
const bridgeMemoryLimit = 256 << 20

type bridgeMemory struct {
	mu   sync.Mutex
	used uint64
}

func (m *bridgeMemory) Reserve(n uint64) (func(), error) {
	m.mu.Lock()
	if n > bridgeMemoryLimit || m.used > bridgeMemoryLimit-n {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: adapter subscription bytes=%d", ethguard.ErrHeadMemoryBudget, n)
	}
	m.used += n
	m.mu.Unlock()
	return sync.OnceFunc(func() { m.mu.Lock(); m.used -= n; m.mu.Unlock() }), nil
}

type incomingAdmission struct {
	memory   *bridgeMemory
	bytes    uint64
	rejected error
}

func (a *incomingAdmission) Reserve(n uint64) (func(), error) {
	release, err := a.memory.Reserve(n)
	if err == nil {
		a.bytes = n
	} else {
		a.rejected = err
	}
	return release, err
}

type deliveryBatch[T any] struct {
	values  []T
	release func()
}

type channelSubscription[T any] struct {
	life            context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	err             chan error
	queue           chan deliveryBatch[T]
	memory          bridgeMemory
	mu              sync.Mutex
	stopped         bool
	cause           error
	coreErr         error
	adapterErr      error
	terminalRelease func()
	detach          func()
}

func newChannelSubscription[T any]() *channelSubscription[T] {
	life, cancel := context.WithCancel(context.Background())
	return &channelSubscription[T]{life: life, cancel: cancel, done: make(chan struct{}), err: make(chan error, 1), queue: make(chan deliveryBatch[T], bridgeQueueCapacity)}
}
func (s *channelSubscription[T]) Done() <-chan struct{} { return s.done }
func (s *channelSubscription[T]) Err() <-chan error     { return s.err }
func (s *channelSubscription[T]) Stop()                 { s.stop(nil, nil) }
func (s *channelSubscription[T]) Unsubscribe()          { s.Stop(); <-s.done }
func (s *channelSubscription[T]) stop(coreErr, adapterErr error) bool {
	accepted := false
	s.mu.Lock()
	if !s.stopped {
		accepted = true
		s.stopped = true
		s.coreErr = coreErr
		s.adapterErr = adapterErr
		s.cause = coreErr
		if adapterErr != nil {
			s.cause = adapterErr
		}
		s.cancel()
	}
	s.mu.Unlock()
	return accepted
}
func (s *channelSubscription[T]) complete() {
	if s.cause != nil {
		// Enqueueing in the caller-visible buffered channel transfers ownership;
		// completion does not wait for the caller to receive or acknowledge it.
		s.err <- s.cause
	}
	if s.terminalRelease != nil {
		s.terminalRelease()
		s.terminalRelease = nil
	}
	s.cause, s.coreErr, s.adapterErr = nil, nil, nil
	close(s.err)
	close(s.done)
	if s.detach != nil {
		s.detach()
	}
}
func (s *channelSubscription[T]) send(ch chan<- T) {
	for {
		select {
		case <-s.life.Done():
			return
		case batch := <-s.queue:
			func() {
				defer batch.release()
				for _, value := range batch.values {
					if s.life.Err() != nil {
						return
					}
					select {
					case ch <- value:
					case <-s.life.Done():
						return
					}
				}
			}()
		}
	}
}

// startChannel registers before setup so Core.Close joins even a blocked setup.
// Native ownership is admitted before Next returns. Conversion gets a second
// reservation (8 times the admitted native graph plus 4 KiB) while both forms
// coexist. Only converted batches cross the two-slot handoff. Total bridge
// ownership, including sender and converter, never exceeds 256 MiB.
func startChannel[E, T any](c *Client, ctx context.Context, method string, ch chan<- T, preErr error, setup func(context.Context) (nativeSubscription[E], error), convert func(E) ([]T, ConversionReport, error), audit func(E) *SubscriptionAudit) (ethereum.Subscription, error) {
	started := time.Time{}
	if c.observing() {
		started = time.Now()
	}
	if preErr == nil && (c == nil || c.core == nil) {
		preErr = conversionError("Client", "core", "nil execution owner", nil)
	}
	if preErr == nil && ch == nil {
		preErr = fmt.Errorf("%w: nil subscription channel", ethguard.ErrInvalidRequest)
	}
	if preErr != nil {
		if c.observing() {
			c.observeSubscription(ctx, method, started, &SubscriptionAudit{Phase: "setup"}, ConversionReport{}, nil, preErr)
		}
		return nil, preErr
	}
	s := newChannelSubscription[T]()
	detach, err := c.core.AttachSubscription(s)
	if err != nil {
		s.Stop()
		s.complete()
		if c.observing() {
			c.observeSubscription(ctx, method, started, &SubscriptionAudit{Phase: "setup"}, ConversionReport{}, err, nil)
		}
		return nil, err
	}
	s.detach = detach
	setupCtx, cancelSetup := context.WithCancel(ctx)
	stopSetup := context.AfterFunc(s.life, cancelSetup)
	native, err := setup(setupCtx)
	if err != nil && s.life.Err() != nil {
		err = ethguard.ErrPoolClosed
	}
	stopSetup()
	cancelSetup()
	if err != nil {
		s.stop(err, nil)
		s.complete()
		if c.observing() {
			c.observeSubscription(ctx, method, started, &SubscriptionAudit{Phase: "setup"}, ConversionReport{}, err, nil)
		}
		return nil, err
	}
	basis := native.Basis()
	fixed := uint64(16384 + len(basis.DomainID) + cap(basis.Members)*16)
	for _, member := range basis.Members {
		fixed += uint64(len(member))
	}
	fixedRelease, err := s.memory.Reserve(fixed)
	if err == nil {
		if s.life.Err() != nil {
			err = ethguard.ErrPoolClosed
		}
	}
	if err != nil {
		if fixedRelease != nil {
			fixedRelease()
		}
		_ = native.Close()
		s.stop(err, nil)
		s.complete()
		if c.observing() {
			c.observeSubscription(ctx, method, started, &SubscriptionAudit{Phase: "setup", Basis: basis}, ConversionReport{}, err, nil)
		}
		return nil, err
	}
	var observationCtx context.Context
	if c.observing() {
		// Stream observations outlive setup cancellation while retaining values
		// from the caller's context. Setup observations keep the original context.
		observationCtx = context.WithoutCancel(ctx)
		c.observeSubscription(ctx, method, started, &SubscriptionAudit{Phase: "setup", Basis: basis}, ConversionReport{}, nil, nil)
	}
	go func() {
		var workers sync.WaitGroup
		workers.Add(3)
		go func() { defer workers.Done(); s.send(ch) }()
		go func() {
			defer workers.Done()
			select {
			case <-s.life.Done():
			case <-native.Terminated():
				s.stop(native.TerminalError(), nil)
			}
		}()
		go func() {
			defer workers.Done()
			for s.life.Err() == nil {
				admission := &incomingAdmission{memory: &s.memory}
				event, sourceRelease, nextErr := native.NextWithBudget(s.life, admission)
				if nextErr != nil {
					// A returned terminal/transfer error may itself own audit. Keep its
					// reservation until complete publishes it in the buffered Err channel.
					cause := nextErr
					adapterCause := error(nil)
					if admission.rejected != nil {
						cause = nil
						adapterCause = nextErr
					}
					ownsAudit := true
					select {
					case <-native.Terminated():
						cause = native.TerminalError()
						adapterCause = nil
						ownsAudit = false
					default:
					}
					if s.stop(cause, adapterCause) && ownsAudit {
						s.terminalRelease = sourceRelease
					} else if sourceRelease != nil {
						sourceRelease()
					}
					return
				}
				var convertedRelease func()
				var adapterErr error
				if nativeRetraction(event) {
					adapterErr = ethguard.ErrReorgDetected
				} else if admission.bytes > (^uint64(0)-4096)/8 {
					adapterErr = ethguard.ErrHeadMemoryBudget
				} else {
					convertedRelease, adapterErr = s.memory.Reserve(admission.bytes*8 + 4096)
				}
				var values []T
				var report ConversionReport
				if adapterErr == nil {
					values, report, adapterErr = convert(event)
				}
				if adapterErr != nil {
					s.stop(nil, adapterErr)
				}
				if c.observing() {
					detail := audit(event)
					detail.Phase = "event"
					detail.Basis = basis
					c.observeSubscription(observationCtx, method, started, detail, report, nil, adapterErr)
				}
				sourceRelease()
				if adapterErr != nil {
					if convertedRelease != nil {
						convertedRelease()
					}
					return
				}
				if len(values) == 0 {
					convertedRelease()
					continue
				}
				batch := deliveryBatch[T]{values: values, release: convertedRelease}
				select {
				case <-s.life.Done():
					batch.release()
					return
				case s.queue <- batch:
				default:
					batch.release()
					s.stop(nil, fmt.Errorf("%w: adapter subscription handoff full", ethguard.ErrHeadMemoryBudget))
					return
				}
			}
		}()
		workers.Wait()
		_ = native.Close()
		for {
			select {
			case batch := <-s.queue:
				batch.release()
			default:
				goto drained
			}
		}
	drained:
		if c.observing() {
			c.observeSubscription(observationCtx, method, started, &SubscriptionAudit{Phase: "terminal", Basis: basis}, ConversionReport{}, s.coreErr, s.adapterErr)
		}
		fixedRelease()
		s.complete()
	}()
	return s, nil
}

// SubscribeNewHead bridges atomic native changes to geth headers. Any native
// retraction terminates with ErrReorgDetected; caller channels are never closed.
func (c *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	return startChannel(c, ctx, "eth_subscribe:newHeads", ch, nil, func(setup context.Context) (nativeSubscription[ethguard.HeadEvent], error) {
		return c.core.SubscribeNewHead(setup, ethguard.DefaultHeadSubscriptionPolicy())
	}, func(event ethguard.HeadEvent) ([]*types.Header, ConversionReport, error) {
		if len(event.Path.Retracted) != 0 {
			return nil, ConversionReport{}, ethguard.ErrReorgDetected
		}
		out := make([]*types.Header, len(event.Path.Appended))
		var report ConversionReport
		for i, delivery := range event.Path.Appended {
			value, r, err := ToGethHeader(delivery.Header)
			report.merge(r)
			if err != nil {
				return nil, report, err
			}
			out[i] = value
		}
		return out, report, nil
	}, func(event ethguard.HeadEvent) *SubscriptionAudit { return &SubscriptionAudit{Head: &event} })
}

// SubscribeFilterLogs terminates on every native retraction, including blocks
// that contained no matching logs. Native path evidence remains in the audit.
func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	filter, _, err := FromGethFilterQuery(q)
	return startChannel(c, ctx, "eth_subscribe:logs", ch, err, func(setup context.Context) (nativeSubscription[ethguard.LogsEvent], error) {
		return c.core.SubscribeFilterLogs(setup, filter, ethguard.DefaultLogsSubscriptionPolicy())
	}, func(event ethguard.LogsEvent) ([]types.Log, ConversionReport, error) {
		if len(event.Path.Retracted) != 0 {
			return nil, ConversionReport{}, ethguard.ErrReorgDetected
		}
		count := 0
		for _, delivery := range event.Path.Appended {
			count += len(delivery.Logs)
		}
		out := make([]types.Log, 0, count)
		var report ConversionReport
		for _, delivery := range event.Path.Appended {
			for _, log := range delivery.Logs {
				value, r, err := ToGethLog(log)
				report.merge(r)
				if err != nil {
					return nil, report, err
				}
				out = append(out, value)
			}
		}
		return out, report, nil
	}, func(event ethguard.LogsEvent) *SubscriptionAudit { return &SubscriptionAudit{Logs: &event} })
}

// SubscribeTransactionReceipts emits one complete receipt slice per appended
// block. Empty selected blocks retain an empty slice; retractions terminate.
func (c *Client) SubscribeTransactionReceipts(ctx context.Context, q *ethereum.TransactionReceiptsQuery, ch chan<- []*types.Receipt) (ethereum.Subscription, error) {
	var filter *ethapi.TransactionReceiptsQuery
	if q != nil {
		hashes := make([]ethapi.Hash, len(q.TransactionHashes))
		for i, hash := range q.TransactionHashes {
			hashes[i] = ethapi.Hash(hash.Hex())
		}
		filter = &ethapi.TransactionReceiptsQuery{TransactionHashes: &hashes}
	}
	return startChannel(c, ctx, "eth_subscribe:transactionReceipts", ch, nil, func(setup context.Context) (nativeSubscription[ethguard.ReceiptsEvent], error) {
		return c.core.SubscribeTransactionReceipts(setup, filter, ethguard.DefaultReceiptsSubscriptionPolicy())
	}, func(event ethguard.ReceiptsEvent) ([][]*types.Receipt, ConversionReport, error) {
		if len(event.Path.Retracted) != 0 {
			return nil, ConversionReport{}, ethguard.ErrReorgDetected
		}
		out := make([][]*types.Receipt, len(event.Path.Appended))
		var report ConversionReport
		for i, delivery := range event.Path.Appended {
			out[i] = make([]*types.Receipt, len(delivery.Receipts))
			for j, receipt := range delivery.Receipts {
				value, r, err := ToGethReceipt(&receipt)
				report.merge(r)
				if err != nil {
					return nil, report, err
				}
				out[i][j] = value
			}
		}
		return out, report, nil
	}, func(event ethguard.ReceiptsEvent) *SubscriptionAudit { return &SubscriptionAudit{Receipts: &event} })
}

func nativeRetraction[E any](event E) bool {
	switch event := any(event).(type) {
	case ethguard.HeadEvent:
		return len(event.Path.Retracted) > 0
	case ethguard.LogsEvent:
		return len(event.Path.Retracted) > 0
	case ethguard.ReceiptsEvent:
		return len(event.Path.Retracted) > 0
	}
	return false
}

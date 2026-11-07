package ethguard

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
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

func (c *Client) subscriptionSource(_ context.Context) (subscriptionSource, error) {
	source, err := c.pool.SubscriptionSource()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrInvalidRequest, err)
	}
	return source, nil
}

func startSubscription[T any](ctx context.Context, inner transport.Subscription, out chan<- T, decode func([]byte) (T, error)) ethereum.Subscription {
	s := &ethSubscription{inner: inner, stop: make(chan struct{}), done: make(chan struct{}), errs: make(chan error, 1)}
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
	return out, nil
}

type identifiedSubscriptionSource interface {
	subscriptionSource
	ID() string
}

type subscriptionConnectionResetter interface {
	ResetConnection()
}

type aggregateEthSubscription[T any] struct {
	inner       []transport.Subscription
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
		inners := append([]transport.Subscription(nil), s.inner...)
		s.innerMu.Unlock()
		for _, inner := range inners {
			go inner.Unsubscribe()
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

func startAggregatedSubscription[T any](ctx context.Context, sources []identifiedSubscriptionSource, kind internalsubscription.Kind, args []any, out chan<- T, decode func([]byte, string) (internalsubscription.Observation, T, error)) (ethereum.Subscription, error) {
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
	merged, cancel := context.WithCancel(ctx)
	s := &aggregateEthSubscription[T]{agg: state, stop: make(chan struct{}), done: make(chan struct{}), errs: make(chan error, 1), cancel: cancel, out: out, decode: decode, faults: make(chan error, len(sources)), doneSources: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			s.finish(ctx.Err())
		case <-s.stop:
		}
	}()
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			s.finish(makeSubscriptionFault("", 1, "lifecycle", "", err))
			return nil, err
		}
		inner, subErr := source.Subscribe(merged, string(kind), args...)
		if subErr != nil {
			s.finish(makeSubscriptionFault(source.ID(), 1, "subscribe", "", subErr))
			return nil, subErr
		}
		s.inner = append(s.inner, inner)
	}
	var sourceWG sync.WaitGroup
	sourceWG.Add(len(s.inner))
	for i, inner := range s.inner {
		sourceID := members[i]
		go func() {
			defer sourceWG.Done()
			events, errs := inner.Events(), inner.Err()
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
					return
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
					if event.Generation != 0 && event.Generation != 1 {
						continue
					}
					observation, _, decodeErr := s.decode(event.Result, sourceID)
					if decodeErr != nil {
						s.finish(makeSubscriptionFault(sourceID, 1, "decode", "", decodeErr))
						return
					}
					if event.Generation == 0 {
						event.Generation = 1
					}
					observation.Generation = event.Generation
					if submitErr := s.agg.Submit(observation); submitErr != nil && !errors.Is(submitErr, internalsubscription.ErrStaleGeneration) && !errors.Is(submitErr, internalsubscription.ErrUnknownSource) {
						s.finish(makeSubscriptionFault(sourceID, observation.Generation, "aggregate", observation.ContextKey, submitErr))
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
		return startSubscription(ctx, inner, ch, func(raw []byte) (*types.Header, error) {
			return typed.DecodeHeader(raw)
		}), nil
	}
	return startAggregatedSubscription(ctx, sources, internalsubscription.KindNewHeads, nil, ch, func(raw []byte, sourceID string) (internalsubscription.Observation, *types.Header, error) {
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
	})
}

func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	arg, err := subscriptionFilterArg(q)
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
		return startSubscription(ctx, inner, ch, func(raw []byte) (types.Log, error) {
			return typed.DecodeLog(raw)
		}), nil
	}
	return startAggregatedSubscription(ctx, sources, internalsubscription.KindLogs, []any{arg}, ch, func(raw []byte, sourceID string) (internalsubscription.Observation, types.Log, error) {
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
		inner, subErr := sources[0].Subscribe(ctx, "transactionReceipts", q)
		if subErr != nil {
			return nil, subErr
		}
		return startSubscription(ctx, inner, ch, func(raw []byte) ([]*types.Receipt, error) {
			return typed.DecodeReceipts(raw)
		}), nil
	}
	return startAggregatedSubscription(ctx, sources, internalsubscription.KindTransactionReceipts, []any{q}, ch, func(raw []byte, sourceID string) (internalsubscription.Observation, []*types.Receipt, error) {
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
	})
}

func subscriptionFilterArg(q ethereum.FilterQuery) (map[string]any, error) {
	arg := make(map[string]any)
	if len(q.Addresses) > 0 {
		arg["address"] = q.Addresses
	}
	if q.Topics != nil {
		arg["topics"] = q.Topics
	}
	if q.BlockHash != nil {
		if q.FromBlock != nil || q.ToBlock != nil {
			return nil, errors.New("cannot specify both BlockHash and FromBlock/ToBlock")
		}
		arg["blockHash"] = *q.BlockHash
		return arg, nil
	}
	if q.FromBlock == nil {
		arg["fromBlock"] = "0x0"
	} else {
		value, err := checkedBlockNumArg(q.FromBlock)
		if err != nil {
			return nil, err
		}
		arg["fromBlock"] = value
	}
	if q.ToBlock == nil {
		arg["toBlock"] = "latest"
	} else {
		value, err := checkedBlockNumArg(q.ToBlock)
		if err != nil {
			return nil, err
		}
		arg["toBlock"] = value
	}
	return arg, nil
}

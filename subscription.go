package ethguard

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
)

// ethSubscription projects a transport subscription into the official
// ethereum.Subscription lifecycle while a typed forwarding goroutine owns
// decoding and channel delivery.
type ethSubscription struct {
	inner transport.Subscription
	stop  chan struct{}
	done  chan struct{}
	errs  chan error
	once  sync.Once
}

func (s *ethSubscription) Err() <-chan error { return s.errs }

func (s *ethSubscription) Unsubscribe() {
	s.once.Do(func() {
		close(s.stop)
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

func (c *Client) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	source, err := c.subscriptionSource(ctx)
	if err != nil {
		return nil, err
	}
	inner, err := source.Subscribe(ctx, "newHeads")
	if err != nil {
		return nil, err
	}
	return startSubscription(ctx, inner, ch, func(raw []byte) (*types.Header, error) {
		return typed.DecodeHeader(raw)
	}), nil
}

func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	arg, err := subscriptionFilterArg(q)
	if err != nil {
		return nil, err
	}
	source, err := c.subscriptionSource(ctx)
	if err != nil {
		return nil, err
	}
	inner, err := source.Subscribe(ctx, "logs", arg)
	if err != nil {
		return nil, err
	}
	return startSubscription(ctx, inner, ch, func(raw []byte) (types.Log, error) {
		return typed.DecodeLog(raw)
	}), nil
}

func (c *Client) SubscribeTransactionReceipts(ctx context.Context, q *ethereum.TransactionReceiptsQuery, ch chan<- []*types.Receipt) (ethereum.Subscription, error) {
	if ch == nil {
		return nil, fmt.Errorf("%w: nil subscription channel", rpc.ErrInvalidRequest)
	}
	source, err := c.subscriptionSource(ctx)
	if err != nil {
		return nil, err
	}
	inner, err := source.Subscribe(ctx, "transactionReceipts", q)
	if err != nil {
		return nil, err
	}
	return startSubscription(ctx, inner, ch, func(raw []byte) ([]*types.Receipt, error) {
		return typed.DecodeReceipts(raw)
	}), nil
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

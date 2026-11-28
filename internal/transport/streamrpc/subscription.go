package streamrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// Subscription is the connection-side handle returned by Subscribe. It keeps the
// notification stream and the server-side subscription ID together so
// Unsubscribe can be idempotent and always releases both resources.
type Subscription struct {
	transport *Transport
	stream    *NotificationStream
	id        string
	once      sync.Once
}

func (s *Subscription) Events() <-chan Notification { return s.stream.Events() }
func (s *Subscription) Err() <-chan error           { return s.stream.Err() }

func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		s.stream.Unsubscribe()
		// Best effort: the stream is always closed locally even when the
		// server-side unsubscribe cannot be delivered because the socket died.
		if s.transport != nil {
			s.transport.unsubscribeServer(s.id)
		}
	})
}

func (t *Transport) unsubscribeServer(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	_ = t.Call(ctx, "eth_unsubscribe", id).Decode(&ok)
}

// Subscribe opens an eth namespace subscription and binds its notifications
// to a bounded stream. It is intentionally outside the unary Transport
// interface; callers must opt into the subscription capability explicitly.
func (t *Transport) Subscribe(ctx context.Context, kind string, args ...any) (*Subscription, error) {
	return t.subscribe(ctx, transport.NotificationPolicy{Messages: 256, Bytes: defaultNotificationBytes, MaxMessageBytes: defaultNotificationBytes}, kind, args...)
}
func (t *Transport) SubscribeBounded(ctx context.Context, p transport.NotificationPolicy, kind string, args ...any) (transport.Subscription, error) {
	sub, err := t.subscribe(ctx, p, kind, args...)
	if err != nil {
		return nil, err
	}
	return sub, nil
}
func (t *Transport) subscribe(ctx context.Context, policy transport.NotificationPolicy, kind string, args ...any) (*Subscription, error) {
	if kind == "" {
		return nil, fmt.Errorf("streamrpc: empty subscription kind")
	}
	// The setup context bounds eth_subscribe only. The stream remains alive
	// until Unsubscribe, owner Close, or a transport failure. It registers
	// with the pending placeholder from birth, so no other subscription's
	// notification can land here during the eth_subscribe round trip; the
	// first server push for this subscription (which can outrun the
	// response) buffers at the transport level and is flushed on bind.
	stream, err := t.openNotificationStreamPolicy(context.WithoutCancel(ctx), "", 0, policy, pendingSubscriptionID)
	if err != nil {
		return nil, err
	}
	var id string
	params := make([]any, 0, len(args)+1)
	params = append(params, kind)
	params = append(params, args...)
	response, requestWritten := t.call(ctx, "eth_subscribe", params...)
	if err := response.Decode(&id); err != nil {
		stream.Unsubscribe()
		if ctx.Err() != nil {
			err = t.cancelSubscribeSetup(response, requestWritten, err)
		}
		return nil, err
	}
	if id == "" {
		stream.Unsubscribe()
		return nil, fmt.Errorf("streamrpc: eth_subscribe returned empty id")
	}
	if !stream.bindSubscriptionID(id) {
		stream.mu.Lock()
		cause := stream.terminal
		stream.mu.Unlock()
		stream.Unsubscribe()
		t.unsubscribeServer(id)
		if cause == nil {
			cause = ErrNotificationBackpressure
		}
		return nil, cause
	}
	return &Subscription{transport: t, stream: stream, id: id}, nil
}

// cancelSubscribeSetup retires only resources that the canceled call could
// have created. The response can retain an associated answer even when Decode
// returns the exchange's cancellation; consume that answer without replacing
// its communication facts.
func (t *Transport) cancelSubscribeSetup(response rpc.Response, requestWritten bool, cause error) error {
	if !requestWritten || response.Answer.Kind == rpc.AnswerProtocolError {
		return cause
	}
	if response.Answer.Kind == rpc.AnswerResult {
		var id string
		if err := json.Unmarshal(response.Answer.Result, &id); err == nil && id != "" {
			t.unsubscribeServer(id)
			return cause
		}
	}
	// Once written, an unanswered request may have created a server-side
	// subscription with an unknown ID. Retiring the socket releases it.
	err := fmt.Errorf("streamrpc: subscribe setup: %w: %w", cause, transport.ErrConnectionClosed)
	t.abort(err)
	return err
}

// SubscribeCapability adapts the concrete stream subscription to the optional
// transport capability without widening the unary Transport interface.
func (t *Transport) SubscribeCapability(ctx context.Context, kind string, args ...any) (transport.Subscription, error) {
	sub, err := t.Subscribe(ctx, kind, args...)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

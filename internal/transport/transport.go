// Package transport is the Port-only physical home of the connection seam:
// the Transport and Dialer interfaces pool consumes, and the neutral
// sentinels core packages classify on. It owns no adapter implementation —
// native adapters live in internal/transport/{http,wss,ipc}.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/codermaybe/ethguard/rpc"
)

// Transport is one long-lived upstream connection. It deliberately exposes
// only a raw JSON-RPC call, the chain identity probe used by source admission,
// and lifecycle close. Each call returns
// communication progress and a distinct protocol answer in rpc.Response.
type Transport interface {
	Call(context.Context, string, ...any) rpc.Response
	ChainID(context.Context) (*big.Int, error)
	Close()
}

// Dialer creates one long-lived Transport for an endpoint.
type Dialer interface {
	DialContext(context.Context, string) (Transport, error)
}

// BoundedSubscriberDialer declares subscription capability without opening a
// connection. Pool snapshots this property at construction, then checks the
// actual Transport's BoundedSubscriber capability when establishing a stream.
type BoundedSubscriberDialer interface {
	Dialer
	SupportsBoundedSubscriptions() bool
}

// Notification is one decoded JSON-RPC subscription notification. It is an
// optional capability beside the unary Transport surface; adapters that do
// not support subscriptions need not implement Subscriber.
type Notification struct {
	SourceID       string
	Generation     uint64
	Method         string
	SubscriptionID string
	Result         json.RawMessage
	Raw            json.RawMessage
	ReceivedAt     time.Time
	// Release returns raw-byte ownership after the receiver finishes decoding.
	Release func()
}

type NotificationPolicy struct {
	Messages               int
	Bytes, MaxMessageBytes uint64
	Reserve                func(uint64) (func(), error)
}
type BoundedSubscriber interface {
	SubscribeBounded(context.Context, NotificationPolicy, string, ...any) (Subscription, error)
}

// Subscription is the transport-neutral lifecycle surface consumed by the
// subscription owner. Notifications retain their raw JSON for that owner to
// validate and project.
type Subscription interface {
	Events() <-chan Notification
	Err() <-chan error
	Unsubscribe()
}

// Subscriber is an optional transport capability. It deliberately lives
// beside Transport so adding subscriptions does not widen every unary fake
// and adapter in the repository.
type Subscriber interface {
	SubscribeCapability(context.Context, string, ...any) (Subscription, error)
}

// ErrConnectionClosed is the neutral sentinel for a call attempted on a
// transport after Close. Every adapter maps its own closed-connection error
// onto this sentinel so core packages classify the lifecycle without knowing
// the adapter. Close is idempotent, and calls already in flight are governed
// by their own context, not by Close.
var ErrConnectionClosed = errors.New("transport: connection closed")

// ErrBodyTooLarge classifies a response body that exceeded the capture or
// read byte-count limit. An oversized body is never handed to a decoder as if
// it were a complete JSON-RPC response: the transport fails the call with
// this error instead, so no adapter vouches for truncated bytes.
var ErrBodyTooLarge = errors.New("transport: response body exceeds limit")

// ErrNotificationBackpressure identifies a bounded notification queue overflow.
var ErrNotificationBackpressure = errors.New("transport: notification backpressure")

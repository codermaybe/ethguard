// Package streamrpc owns duplex JSON-RPC calls and bounded subscriptions.
// Physical connections own framing, write serialization and cancellation.
package streamrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
)

// MessageConn transfers independently owned complete messages. Read failure
// is terminal. Close immediately unblocks reads, writes and lock waiters.
// WriteMessage reports whether any bytes reached the physical connection;
// (false, ctx.Err()) alone denotes cancellation that kept it reusable.
// Physical write errors must not be disguised as caller cancellation.
type MessageConn interface {
	ReadMessage() ([]byte, error)
	WriteMessage(context.Context, []byte) (written bool, err error)
	Close() error
}

// Options bounds notifications received before a subscription ID is known.
// Zero values select finite defaults.
type Options struct {
	PendingMessages int
	PendingBytes    uint64
}

var ErrTransportClosed = fmt.Errorf("streamrpc: transport closed: %w", transport.ErrConnectionClosed)
var ErrNotificationBackpressure = transport.ErrNotificationBackpressure

// New takes ownership of conn and starts its sole reader. The connection must
// already be established; no reconnect or replay occurs within this owner.
func New(conn MessageConn, opts Options) *Transport {
	t := &Transport{
		conn: conn, ids: rpcwire.NewIDGen(), pending: make(map[string]chan frameResult),
		streams:              make(map[*NotificationStream]struct{}),
		subscriptionIDs:      make(map[string]*NotificationStream),
		pendingNotifications: make(map[string][]Notification),
		pendingMessagesLimit: opts.PendingMessages, pendingBytesLimit: opts.PendingBytes,
	}
	go t.readLoop()
	return t
}

type frameResult struct {
	frame json.RawMessage
	err   error
}

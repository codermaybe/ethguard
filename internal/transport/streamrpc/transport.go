package streamrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// Transport is one duplex JSON-RPC connection. One reader goroutine decodes
// inbound frames; calls are safe for concurrent use.
type Transport struct {
	conn MessageConn
	ids  *rpcwire.IDGen

	mu                                    sync.Mutex
	pending                               map[string]chan frameResult
	streams                               map[*NotificationStream]struct{}
	subscriptionIDs                       map[string]*NotificationStream
	pendingNotifications                  map[string][]Notification
	pendingNotificationOrder              []string
	setupStreams                          map[*NotificationStream]bool
	pendingMessagesLimit, pendingMessages int
	pendingBytesLimit, pendingBytes       uint64
	closing                               bool
	closed                                bool

	socketOnce sync.Once
}

var _ transport.Transport = (*Transport)(nil)

// Call performs one unary exchange on the shared socket. Only a frame
// associated with this request can advance it beyond Started.
func (t *Transport) Call(ctx context.Context, method string, args ...any) rpc.Response {
	response, _ := t.call(ctx, method, args...)
	return response
}

// call also reports physical request-write progress for subscription
// setup cleanup. ExchangeStarted alone cannot supply this fact: execution
// starts before acquiring the writer, and does not promise delivery.
func (t *Transport) call(ctx context.Context, method string, args ...any) (rpc.Response, bool) {
	exchange := rpc.Exchange{}
	failed := func(err error) rpc.Response {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		exchange.Cause = err
		return rpc.Response{Exchange: exchange}
	}
	if err := ctx.Err(); err != nil {
		return failed(err), false
	}
	t.mu.Lock()
	closed := t.closed || t.closing
	t.mu.Unlock()
	if closed {
		return failed(ErrTransportClosed), false
	}
	id := t.ids.Next()
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		return failed(fmt.Errorf("streamrpc: encode request: %w", err)), false
	}
	waiter := make(chan frameResult, 1)
	t.mu.Lock()
	if t.closed || t.closing {
		t.mu.Unlock()
		return failed(ErrTransportClosed), false
	}
	t.pending[string(id)] = waiter
	t.mu.Unlock()
	exchange.Progress = rpc.ExchangeStarted
	written, err := t.write(ctx, body)
	if err != nil {
		t.removePending(string(id))
		return failed(err), written
	}
	complete := func(frame frameResult) rpc.Response {
		if frame.err != nil {
			return failed(frame.err)
		}
		exchange.Progress = rpc.ExchangeCompleted
		exchange.Cause = ctx.Err()
		return rpcwire.CompleteCall(frame.frame, id, exchange)
	}
	select {
	case frame := <-waiter:
		return complete(frame), true
	case <-ctx.Done():
		// Synchronize removal with read-loop association. An already associated
		// frame remains a fact even when cancellation wins final error delivery.
		t.mu.Lock()
		delete(t.pending, string(id))
		select {
		case frame := <-waiter:
			t.mu.Unlock()
			return complete(frame), true
		default:
			t.mu.Unlock()
			t.drainCloseIfSettled()
			return failed(ctx.Err()), true
		}
	}
}

// write consumes the physical connection's write result. Only cancellation
// before any bytes move is recoverable; a partial or physical failed write
// cannot promise a synchronized message boundary.
func (t *Transport) write(ctx context.Context, body []byte) (bool, error) {
	written, err := t.conn.WriteMessage(ctx, body)
	if err == nil && written {
		return true, nil
	}
	if !written && ctx.Err() != nil && err == ctx.Err() { //nolint:errorlint // Exact cancellation alone is reusable; physical errors wrapping it must abort.
		return false, err
	}
	if err == nil {
		err = errors.New("MessageConn returned (false, nil)")
	}
	writeErr := fmt.Errorf("streamrpc: write request: %w: %w", err, transport.ErrConnectionClosed)
	t.abort(writeErr)
	return written, writeErr
}

// drainCloseIfSettled marks the transport closed and closes the socket when
// a requested close has drained its last pending response. The drain
// condition is the one Close-vs-pending rule both the delivered-response and
// the close paths apply.
func (t *Transport) drainCloseIfSettled() {
	t.mu.Lock()
	closeSocket := t.closing && !t.closed && len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	if closeSocket {
		t.closeSocket()
	}
}

func (t *Transport) removePending(id string) {
	t.mu.Lock()
	delete(t.pending, id)
	t.mu.Unlock()
	t.drainCloseIfSettled()
}

// readLoop owns the socket's read side. A delivered frame is matched to its
// pending call by request ID; notifications and unknown IDs are ignored. Any
// read failure ends the connection: a partial or oversized frame cannot be
// resynchronized, so every pending call fails and new calls are rejected.
//
// The ID is read with a streaming decoder so a frame carrying a valid prefix
// and trailing garbage still reaches its caller; rpcwire, not the demux,
// owns the malformed-response classification.
func (t *Transport) readLoop() {
	for {
		message, err := t.conn.ReadMessage()
		if err != nil {
			t.abort(t.classifyReadError(err))
			return
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.NewDecoder(bytes.NewReader(message)).Decode(&envelope) != nil || len(envelope.ID) == 0 {
			t.dispatchNotification(message)
			continue
		}
		id := string(envelope.ID)
		t.mu.Lock()
		waiter := t.pending[id]
		delete(t.pending, id)
		if waiter != nil {
			waiter <- frameResult{frame: message}
		}
		t.mu.Unlock()
		if waiter != nil {
			// MessageConn delivered this message in an independent buffer that
			// nobody else holds: the envelope's id is consumed before the
			// handover, notifications decode into their own copies, and the
			// pending entry was deleted above so this is the only delivery.
			// Ownership transfers to the waiter, which reads the frame once
			// inside rpcwire.CompleteCall.
			t.drainCloseIfSettled()
		}
	}
}

func (t *Transport) classifyReadError(err error) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return ErrTransportClosed
	}
	return fmt.Errorf("streamrpc: connection lost: %w: %w", err, transport.ErrConnectionClosed)
}

// abort marks the connection unusable and fails every pending call with the
// same cause. It is the single fan-out point for read limits, peer
// disconnects, and write failures. Closing the socket here also unblocks a
// reader still waiting on a broken write path, so the goroutine cannot leak.
func (t *Transport) abort(err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	// The first closer owns failure delivery. Closing the socket wakes the
	// reader; its consequential read error must not replace this cause in
	// notification streams before this abort reaches their finish calls.
	t.closed = true
	t.closing = true
	pending := t.pending
	t.pending = make(map[string]chan frameResult)
	t.clearPendingNotificationsLocked()
	streams := make([]*NotificationStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()
	t.closeSocket()
	for _, stream := range streams {
		stream.finish(err)
	}
	for _, waiter := range pending {
		waiter <- frameResult{err: err}
	}
}

func (t *Transport) closeSocket() {
	t.socketOnce.Do(func() { _ = t.conn.Close() })
}

// ChainID implements transport.Transport through a plain eth_chainId call.
func (t *Transport) ChainID(ctx context.Context) (*big.Int, error) {
	var raw string
	if err := t.Call(ctx, "eth_chainId").Decode(&raw); err != nil {
		return nil, err
	}
	value, err := rpc.ParseHexQuantity(raw)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetUint64(value), nil
}

// Close blocks new calls and releases the socket after all in-flight calls
// finish or cancel. In-flight calls remain governed by their own contexts,
// matching the transport Port contract; a peer failure can still abort them
// immediately. Close is idempotent.
func (t *Transport) Close() {
	t.mu.Lock()
	t.closing = true
	streams := make([]*NotificationStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.clearPendingNotificationsLocked()
	closeSocket := len(t.pending) == 0
	if closeSocket {
		t.closed = true
	}
	t.mu.Unlock()
	for _, stream := range streams {
		stream.Unsubscribe()
	}
	if closeSocket {
		t.closeSocket()
	}
}

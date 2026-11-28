package wss

// The owned connection layer: one established WebSocket connection with a
// single reader, serialized writes, and internal control-frame handling. It
// binds a handshake result to the frame codec and exposes exactly the
// operations the raw-call adapter needs — one bounded text write under the
// caller's context, one message read for the reader goroutine, and close.
// Call lifecycle (pending table, demux, abort fan-out) stays in streamrpc;
// this layer owns only the wire mechanics.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
)

// controlWriteTimeout bounds the replies the reader sends outside any call
// context: pong answers and close echoes. A protocol-required reply must not
// wedge the reader forever on a connection that stopped draining; call
// writes keep their own context deadlines. This is a wire-mechanics bound,
// not a keepalive policy.
const controlWriteTimeout = 5 * time.Second

// maxWriteWait bounds one frame write even when the caller's context carries
// no deadline. A write that stalls on a connection the peer stopped draining
// must fail with a write timeout — and release the write lock — instead of
// wedging the reader's control replies and every later call behind it
// forever. Call writes keep their own context deadlines when tighter.
const maxWriteWait = 30 * time.Second

// Conn is one established WebSocket connection. Reads happen on the single
// reader goroutine the adapter starts; writes are serialized across callers
// by one lock. Every physical write error leaves the connection unusable;
// cancellation before writing is recoverable. The RPC owner decides abort.
type Conn struct {
	raw net.Conn
	// reader is the connection's one frame reader. It is sticky: after the
	// first failure every later read returns that failure, so a broken
	// stream can never be misparsed from its middle, no matter how the
	// caller treats the first error.
	reader *frameReader

	// writeSem is the write lock: a capacity-1 channel instead of a mutex
	// so a caller whose context expires while a stalled writer holds the
	// lock can give up instead of queueing forever. Acquire with
	// lockWriter/writeControlLock, release via the returned function.
	writeSem  chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	// closeReplyOnce guards the protocol-violation close reply: a caller
	// that ignores the first error and reads again must not push a second
	// close frame onto the wire.
	closeReplyOnce sync.Once
}

// newConn binds a handshake result into a connection. The limit bounds one
// inbound frame and one assembled message.
func newConn(result *handshakeResult, limit int64) *Conn {
	return &Conn{
		raw:      result.conn,
		reader:   newFrameReader(result.br, clientSide, limit),
		writeSem: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// lockWriter acquires the write lock under the caller's context: a context
// that expires while another writer stalls returns immediately instead of
// queueing on the stalled holder.
func (c *Conn) lockWriter(ctx context.Context) (func(), error) {
	select {
	case c.writeSem <- struct{}{}:
		return func() { <-c.writeSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, transport.ErrConnectionClosed
	}
}

// writeControlLock acquires the write lock without a context. The reader's
// control replies wait for the current writer, whose own deadline (never
// unbounded, see maxWriteWait) releases the lock.
func (c *Conn) writeControlLock() (func(), error) {
	return c.lockWriter(context.Background())
}

// ReadMessage reads one complete text message. It is the single reader
// goroutine's entry point. Control frames are answered internally: ping →
// pong, close → close echo (the received code, or an empty payload when the
// peer sent none). Peer closes surface as CloseError; protocol violations as
// *frameError, answered with at most one close frame per connection before
// the error returns — a caller that reads again gets the sticky failure, not
// a second close; read limit breaches as ErrFrameTooLarge with the
// connection aborted directly — a truncated stream can never be
// resynchronized, so no close-frame handshake is attempted for it.
func (c *Conn) ReadMessage() ([]byte, error) {
	opcode, data, err := c.reader.readMessage(c.handleControl)
	if err != nil {
		if errors.Is(err, ErrFrameTooLarge) {
			return nil, errors.Join(err, transport.ErrBodyTooLarge, transport.ErrConnectionClosed)
		}
		var frameErr *frameError
		switch {
		case errors.As(err, &frameErr):
			c.closeReplyOnce.Do(func() {
				_ = c.writeControl(opClose, closePayload(frameErr.code, ""))
			})
			return nil, err
		default:
			return nil, err
		}
	}
	if opcode != opText {
		// Unreachable: the codec refuses binary data messages with its own
		// protocol error.
		return nil, fmt.Errorf("wss: unexpected message opcode %#x", opcode)
	}
	return data, nil
}

// handleControl answers control frames on behalf of the reader. The returned
// error ends the connection: a close frame becomes CloseError, a failure to
// answer a ping or parse a close is a broken connection.
func (c *Conn) handleControl(opcode byte, payload []byte) error {
	switch opcode {
	case opPing:
		return c.writeControl(opPong, payload)
	case opPong:
		return nil
	case opClose:
		closeFrame, parseErr := parseCloseFrame(payload)
		if parseErr != nil {
			// The parse error is a frameError: ReadMessage's unified
			// protocol-violation path sends the single close reply.
			return parseErr
		}
		// Echo the received code; a close without status is answered with an
		// empty payload, since 1005 must never cross the wire.
		var reply []byte
		if closeFrame.Code != closeNoStatusReceived {
			reply = closePayload(closeFrame.Code, "")
		}
		_ = c.writeControl(opClose, reply)
		return closeFrame
	default:
		return &frameError{code: closeProtocolError, detail: fmt.Sprintf("control opcode %#x is not handled by the connection", opcode)}
	}
}

// WriteMessage writes one masked text frame under the write lock. The
// context's deadline bounds the write; a context without one inherits the
// maxWriteWait ceiling. Cancellation interrupts both lock acquisition and
// physical writing. The bool reports actual transferred bytes, even on error.
func (c *Conn) WriteMessage(ctx context.Context, data []byte) (bool, error) {
	release, err := c.lockWriter(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case <-c.done:
		return false, transport.ErrConnectionClosed
	default:
	}
	deadline := time.Now().Add(maxWriteWait)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.raw.SetWriteDeadline(deadline); err != nil {
		return false, fmt.Errorf("wss: set write deadline: %w", err)
	}
	// Cancellation without a deadline must interrupt a blocked physical write.
	// Join a running callback before releasing the sole writer, so it cannot
	// expire a subsequent caller's deadline after that caller acquired the lock.
	canceled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = c.raw.SetWriteDeadline(time.Now())
		close(canceled)
	})
	defer func() {
		if !stopCancel() {
			<-canceled
		}
	}()
	w := &progressWriter{conn: c.raw}
	err = writeFrame(w, clientSide, opText, data)
	return w.written, err
}

// progressWriter observes actual socket progress, including writes that fail
// after transferring a prefix. A caller's context alone cannot infer this fact.
type progressWriter struct {
	conn    net.Conn
	written bool
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.conn.Write(p)
	if n > 0 {
		w.written = true
	}
	if err != nil {
		// Even a socket returning context.Canceled is a physical failure,
		// distinct from the recoverable exact ctx.Err() pre-write result.
		err = fmt.Errorf("wss: physical write: %w", err)
	}
	return n, err
}

// writeControl sends one control frame under the write lock with the bounded
// control-write deadline.
func (c *Conn) writeControl(opcode byte, payload []byte) error {
	release, err := c.writeControlLock()
	if err != nil {
		return err
	}
	defer release()
	if err := c.raw.SetWriteDeadline(time.Now().Add(controlWriteTimeout)); err != nil {
		return fmt.Errorf("wss: set control write deadline: %w", err)
	}
	err = writeFrame(c.raw, clientSide, opcode, payload)
	_ = c.raw.SetWriteDeadline(time.Time{})
	return err
}

// SetReadDeadline bounds the next read on the connection. The adapter's
// reader runs without deadlines (a live call waits for its context); the
// evidence tooling sets one to bound a single probe.
func (c *Conn) SetReadDeadline(deadline time.Time) error {
	return c.raw.SetReadDeadline(deadline)
}

// Close immediately releases the physical connection and its lock waiters.
// Graceful pending-call drain belongs to the shared RPC transport.
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.raw.Close()
	})
	return err
}

// closePayload encodes a close frame body: a 2-byte code and a UTF-8 reason.
func closePayload(code int, reason string) []byte {
	out := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(out, uint16(code))
	copy(out[2:], reason)
	return out
}

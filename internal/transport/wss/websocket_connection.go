package wss

// The owned connection layer: one established WebSocket connection with a
// single reader, serialized writes, and internal control-frame handling. It
// binds a handshake result to the frame codec and exposes exactly the
// operations the raw-call adapter needs — one bounded text write under the
// caller's context, one message read for the reader goroutine, and close.
// Call lifecycle (pending table, demux, abort fan-out) stays in the adapter;
// this layer owns only the wire mechanics.

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// controlWriteTimeout bounds the replies the reader sends outside any call
// context: pong answers and close echoes. A protocol-required reply must not
// wedge the reader forever on a connection that stopped draining; call
// writes keep their own context deadlines. This is a wire-mechanics bound,
// not a keepalive policy.
const controlWriteTimeout = 5 * time.Second

// Conn is one established WebSocket connection. Reads happen on the single
// reader goroutine the adapter starts; writes are serialized across callers
// by one lock. Every write error leaves the connection unusable — the
// adapter owns the abort decision.
type Conn struct {
	raw   net.Conn
	br    *bufio.Reader
	limit int64

	writeMu sync.Mutex
}

// newConn binds a handshake result into a connection. The limit bounds one
// inbound frame and one assembled message.
func newConn(result *handshakeResult, limit int64) *Conn {
	return &Conn{raw: result.conn, br: result.br, limit: limit}
}

// ReadMessage reads one complete text message. It is the single reader
// goroutine's entry point. Control frames are answered internally: ping →
// pong, close → close echo (the received code, or an empty payload when the
// peer sent none). Peer closes surface as CloseError; protocol violations as
// *frameError, answered with a close frame before the error returns; read
// limit breaches as ErrFrameTooLarge with the connection aborted directly —
// a truncated stream can never be resynchronized, so no close-frame
// handshake is attempted for it.
func (c *Conn) ReadMessage() ([]byte, error) {
	reader := newFrameReader(c.br, clientSide, c.limit)
	opcode, data, err := reader.readMessage(c.handleControl)
	if err != nil {
		var frameErr *frameError
		switch {
		case errors.As(err, &frameErr):
			_ = c.writeControl(opClose, closePayload(frameErr.code, ""))
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
			var frameErr *frameError
			if errors.As(parseErr, &frameErr) {
				_ = c.writeControl(opClose, closePayload(frameErr.code, ""))
			}
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
// context's deadline bounds the write; a context without a deadline clears
// any deadline a previous call left behind. Cancellation while waiting for
// the lock is checked before any bytes move.
func (c *Conn) WriteMessage(ctx context.Context, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.raw.SetWriteDeadline(deadline)
	} else {
		_ = c.raw.SetWriteDeadline(time.Time{})
	}
	return writeFrame(c.raw, clientSide, opText, data)
}

// writeControl sends one control frame under the write lock with the bounded
// control-write deadline.
func (c *Conn) writeControl(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.raw.SetWriteDeadline(time.Now().Add(controlWriteTimeout)); err != nil {
		return fmt.Errorf("wss: set control write deadline: %w", err)
	}
	err := writeFrame(c.raw, clientSide, opcode, payload)
	_ = c.raw.SetWriteDeadline(time.Time{})
	return err
}

// SetReadDeadline bounds the next read on the connection. The adapter's
// reader runs without deadlines (a live call waits for its context); the
// evidence tooling sets one to bound a single probe.
func (c *Conn) SetReadDeadline(deadline time.Time) error {
	return c.raw.SetReadDeadline(deadline)
}

// Close releases the underlying connection. Idempotency and lifecycle
// bookkeeping stay with the adapter's close-once wrapper.
func (c *Conn) Close() error {
	return c.raw.Close()
}

// closePayload encodes a close frame body: a 2-byte code and a UTF-8 reason.
func closePayload(code int, reason string) []byte {
	out := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(out, uint16(code))
	copy(out[2:], reason)
	return out
}

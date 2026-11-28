package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/streamrpc"
)

const maxWriteWait = 30 * time.Second

// Conn owns the IPC stream's physical I/O. One reader and one serialized
// writer are active at a time; RPC state belongs to streamrpc.Transport.
type Conn struct {
	raw       net.Conn
	reader    *scanner
	writeSem  chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newConn(raw net.Conn, limit int64) *Conn {
	return &Conn{raw: raw, reader: newScanner(raw, limit), writeSem: make(chan struct{}, 1), done: make(chan struct{})}
}

func (c *Conn) ReadMessage() ([]byte, error) {
	message, err := c.reader.readMessage()
	if err != nil {
		return nil, fmt.Errorf("ipc: read message: %w: %w", err, transport.ErrConnectionClosed)
	}
	return message, nil
}

func (c *Conn) WriteMessage(ctx context.Context, payload []byte) (bool, error) {
	select {
	case c.writeSem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	case <-c.done:
		return false, transport.ErrConnectionClosed
	}
	defer func() { <-c.writeSem }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case <-c.done:
		return false, transport.ErrConnectionClosed
	default:
	}
	deadline := time.Now().Add(maxWriteWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.raw.SetWriteDeadline(deadline); err != nil {
		return false, fmt.Errorf("ipc: set write deadline: %w", err)
	}
	// Join cancellation before unlocking, so a late callback cannot expire the
	// next writer's deadline. A physical timeout remains a physical failure.
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = c.raw.SetWriteDeadline(time.Now()); close(canceled) })
	defer func() {
		if !stop() {
			<-canceled
		}
	}()
	size, err := writeMessageSize(len(payload))
	if err != nil {
		return false, err
	}
	body := make([]byte, size)
	copy(body, payload)
	body[len(payload)] = '\n'
	n, err := c.raw.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return n > 0, fmt.Errorf("ipc: physical write: %w", err)
	}
	return true, nil
}

// writeMessageSize includes the trailing newline without overflowing the
// allocation size. payloadLen comes from len and is therefore nonnegative.
func writeMessageSize(payloadLen int) (int, error) {
	if payloadLen > math.MaxInt-1 {
		return 0, errors.New("ipc: message length exceeds int capacity")
	}
	return payloadLen + 1, nil
}

func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() { close(c.done); err = c.raw.Close() })
	return err
}

var _ streamrpc.MessageConn = (*Conn)(nil)

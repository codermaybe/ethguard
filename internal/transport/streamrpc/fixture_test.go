package streamrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
)

// scriptedConn is a complete-message test peer. It deliberately has no
// framing: physical transport tests remain with the wire owner.
type scriptedConn struct {
	incoming chan []byte
	outgoing chan []byte
	done     chan struct{}
	once     sync.Once
	writer   chan struct{}
}

func (c *scriptedConn) ReadMessage() ([]byte, error) {
	select {
	case <-c.done:
		return nil, net.ErrClosed
	case b := <-c.incoming:
		return append([]byte(nil), b...), nil
	}
}
func (c *scriptedConn) WriteMessage(ctx context.Context, b []byte) (bool, error) {
	select {
	case c.writer <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	case <-c.done:
		return false, net.ErrClosed
	}
	defer func() { <-c.writer }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case c.outgoing <- append([]byte(nil), b...):
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-c.done:
		return false, net.ErrClosed
	}
}
func (c *scriptedConn) Close() error       { c.once.Do(func() { close(c.done) }); return nil }
func (c *scriptedConn) holdWriter() func() { c.writer <- struct{}{}; return func() { <-c.writer } }

type scriptedPeer struct {
	t        testing.TB
	conn     *scriptedConn
	requests <-chan []byte
}

func newScriptedPeer(t testing.TB, serve func(*scriptedPeer)) *scriptedConn {
	t.Helper()
	c := &scriptedConn{incoming: make(chan []byte, 32), outgoing: make(chan []byte), done: make(chan struct{}), writer: make(chan struct{}, 1)}
	requests := make(chan []byte, 32)
	go func() {
		defer close(requests)
		for {
			select {
			case <-c.done:
				return
			case b := <-c.outgoing:
				select {
				case requests <- b:
				case <-c.done:
					return
				}
			}
		}
	}()
	t.Cleanup(func() { _ = c.Close() })
	go serve(&scriptedPeer{t: t, conn: c, requests: requests})
	return c
}
func (p *scriptedPeer) writeFrame(frame []byte) {
	select {
	case p.conn.incoming <- append([]byte(nil), frame...):
	case <-p.conn.done:
	}
}
func (p *scriptedPeer) respond(request []byte, result string) {
	p.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, requestID(request), result)))
}
func (p *scriptedPeer) respondError(request []byte, code int, message, data string) {
	p.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q%s}}`, requestID(request), code, message, data)))
}
func requestID(request []byte) string {
	var v struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(request, &v)
	return string(v.ID)
}
func notificationFrame(subscription, result string) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, subscription, result))
}
func dialTestTransport(t testing.TB, conn *scriptedConn, _ int64) *Transport {
	t.Helper()
	tr := New(conn, Options{})
	t.Cleanup(tr.Close)
	return tr
}
func (t *Transport) pendingCount() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.pending) }
func openNotificationTestStream(t *testing.T, peer *scriptedConn, ctx context.Context, buffer int) (*Transport, *NotificationStream) {
	t.Helper()
	tr := dialTestTransport(t, peer, 0)
	s, err := tr.OpenNotificationStream(ctx, "source-a", 7, buffer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Unsubscribe)
	return tr, s
}

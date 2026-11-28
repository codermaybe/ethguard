package wss

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/streamrpc"
)

func TestMessageReadsTransferIndependentBuffers(t *testing.T) {
	conn, peer := newPipeConn(t)
	want := [][]byte{
		[]byte(`{"result":"first"}`),
		bytes.Repeat([]byte("a"), 8192),
		[]byte(`{"result":"third"}`),
		bytes.Repeat([]byte("z"), 16384),
	}
	written := make(chan error, 1)
	go func() {
		for i, payload := range want {
			var wire []byte
			if i%2 == 0 {
				wire = rawServerFrame(true, opText, payload)
			} else {
				mid := len(payload) / 2
				wire = append(rawServerFrame(false, opText, payload[:mid]), rawServerFrame(true, opContinuation, payload[mid:])...)
			}
			if _, err := peer.Write(wire); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	var retained [][]byte
	for range want {
		message, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, message)
		for i, previous := range retained {
			if !bytes.Equal(previous, want[i]) {
				t.Fatalf("later message reused delivered buffer %d", i)
			}
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	for i, message := range retained {
		message[0] = '!'
		for j := i + 1; j < len(retained); j++ {
			if !bytes.Equal(retained[j], want[j]) {
				t.Fatalf("caller mutation of buffer %d altered buffer %d", i, j)
			}
		}
	}
}

type observedPhysicalWriter struct {
	net.Conn
	calls atomic.Int32
	write func(int, []byte) (int, error)
}

func (c *observedPhysicalWriter) Write(body []byte) (int, error) {
	return c.write(int(c.calls.Add(1)), body)
}

func TestMessageWriteProgressIncludesHeadersAndRejectsShortWrites(t *testing.T) {
	broken := errors.New("injected physical write failure")
	for _, tc := range []struct {
		name    string
		size    int
		write   func(int, []byte) (int, error)
		written bool
		cause   error
		calls   int32
	}{
		{name: "zero bytes physical error", size: 4, write: func(int, []byte) (int, error) { return 0, broken }, cause: broken, calls: 1},
		{name: "partial small frame error", size: 4, write: func(int, []byte) (int, error) { return 1, broken }, written: true, cause: broken, calls: 1},
		{name: "zero byte short nil", size: 4, write: func(int, []byte) (int, error) { return 0, nil }, cause: io.ErrShortWrite, calls: 1},
		{name: "partial short nil", size: 4, write: func(int, []byte) (int, error) { return 1, nil }, written: true, cause: io.ErrShortWrite, calls: 1},
		{name: "header short nil", size: 8192, write: func(int, []byte) (int, error) { return 1, nil }, written: true, cause: io.ErrShortWrite, calls: 1},
		{name: "header complete payload failed", size: 8192, write: func(n int, body []byte) (int, error) {
			if n == 1 {
				return len(body), nil
			}
			return 0, broken
		}, written: true, cause: broken, calls: 2},
		{name: "header complete payload short nil", size: 8192, write: func(n int, body []byte) (int, error) {
			if n == 1 {
				return len(body), nil
			}
			return len(body) - 1, nil
		}, written: true, cause: io.ErrShortWrite, calls: 2},
		{name: "complete frame", size: 4, write: func(_ int, body []byte) (int, error) { return len(body), nil }, written: true, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, _ := newPipeConn(t)
			raw := &observedPhysicalWriter{Conn: conn.raw, write: tc.write}
			conn.raw = raw
			payload := bytes.Repeat([]byte("x"), tc.size)
			written, err := conn.WriteMessage(t.Context(), payload)
			if written != tc.written || !errors.Is(err, tc.cause) {
				t.Fatalf("write=(%v,%v), want (%v,%v)", written, err, tc.written, tc.cause)
			}
			if raw.calls.Load() != tc.calls {
				t.Fatalf("physical writes=%d, want %d without replay", raw.calls.Load(), tc.calls)
			}
			if !bytes.Equal(payload, bytes.Repeat([]byte("x"), tc.size)) {
				t.Fatal("frame masking mutated the caller's payload")
			}
		})
	}
}

func TestMessageWriteCanceledWhileWaitingForLockReportsNoProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, _ := newPipeConn(t)
		raw := &observedPhysicalWriter{Conn: conn.raw, write: func(_ int, body []byte) (int, error) { return len(body), nil }}
		conn.raw = raw
		release, err := conn.writeControlLock()
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan bool, 1)
		go func() {
			written, err := conn.WriteMessage(ctx, []byte(`{}`))
			done <- !written && errors.Is(err, context.Canceled)
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case ok := <-done:
			if !ok || raw.calls.Load() != 0 {
				t.Fatal("queued cancellation sent bytes or reported physical progress")
			}
		default:
			t.Fatal("queued writer cancellation waited for the lock holder")
		}
	})
}

func TestMessageWriteCancellationWithoutDeadlineStopsPhysicalIO(t *testing.T) {
	for _, prefix := range []int{0, 1, 8} {
		name := map[int]string{0: "zero bytes", 1: "partial header", 8: "complete header"}[prefix]
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn, peer := newPipeConn(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				type result struct {
					written bool
					err     error
				}
				done := make(chan result, 1)
				go func() {
					written, err := conn.WriteMessage(ctx, bytes.Repeat([]byte("x"), 8192))
					done <- result{written, err}
				}()
				if prefix > 0 {
					if _, err := io.ReadFull(peer, make([]byte, prefix)); err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait() // The physical writer is now blocked on net.Pipe.
				started := time.Now()
				cancel()
				synctest.Wait()
				select {
				case got := <-done:
					if got.written != (prefix > 0) || got.err == nil {
						t.Fatalf("prefix=%d result=(%v,%v)", prefix, got.written, got.err)
					}
					if !errors.Is(got.err, context.Canceled) && !errors.Is(got.err, os.ErrDeadlineExceeded) {
						t.Fatalf("unexpected cancellation cause: %v", got.err)
					}
					if time.Since(started) != 0 {
						t.Fatal("cancellation waited for the independent max-write timeout")
					}
				default:
					t.Fatal("WithCancel without a deadline left physical write blocked")
				}
			})
		})
	}
}

type heldCancellationDeadlineConn struct {
	net.Conn
	callbackEntered chan struct{}
	callbackRelease chan struct{}
	once            sync.Once
	writes          atomic.Int32
}

func (c *heldCancellationDeadlineConn) Write(body []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(body)
}

func (c *heldCancellationDeadlineConn) SetWriteDeadline(deadline time.Time) error {
	err := c.Conn.SetWriteDeadline(deadline)
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		c.once.Do(func() { close(c.callbackEntered); <-c.callbackRelease })
	}
	return err
}

func TestCancellationDeadlineCannotOutliveItsWriteLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, peer := newPipeConn(t)
		raw := &heldCancellationDeadlineConn{Conn: conn.raw, callbackEntered: make(chan struct{}), callbackRelease: make(chan struct{})}
		conn.raw = raw
		release := sync.OnceFunc(func() { close(raw.callbackRelease) })
		defer release()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		first := make(chan error, 1)
		go func() { _, err := conn.WriteMessage(ctx, []byte("first")); first <- err }()
		synctest.Wait()
		cancel()
		<-raw.callbackEntered
		synctest.Wait()
		second := make(chan error, 1)
		go func() { _, err := conn.WriteMessage(t.Context(), []byte("second")); second <- err }()
		synctest.Wait()
		if raw.writes.Load() != 1 {
			t.Fatal("later writer acquired the lock while the old deadline callback was still running")
		}
		select {
		case err := <-first:
			t.Fatalf("WriteMessage returned before its cancellation callback settled: %v", err)
		default:
		}
		release()
		synctest.Wait()
		if err := <-first; err == nil {
			t.Fatal("canceled write unexpectedly succeeded")
		}
		reader := newFrameReader(bufio.NewReader(peer), serverSide, DefaultReadLimit)
		_, payload, err := reader.readMessage(nil)
		if err != nil || string(payload) != "second" {
			t.Fatalf("old cancellation poisoned the next frame: %q %v", payload, err)
		}
		if err := <-second; err != nil {
			t.Fatalf("next writer retained an expired deadline: %v", err)
		}
	})
}

func TestCanceledPhysicalWriteWithoutProgressStillRetiresSharedConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, peer := newPipeConn(t)
		tr := streamrpc.New(conn, streamrpc.Options{})
		defer tr.Close()
		stream, err := tr.OpenNotificationStream(t.Context(), "source", 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		pending := make(chan error, 1)
		go func() { pending <- tr.Call(t.Context(), "pending").Decode(nil) }()
		reader := newFrameReader(bufio.NewReader(peer), serverSide, DefaultReadLimit)
		if _, _, err := reader.readMessage(nil); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		canceled := make(chan error, 1)
		go func() { canceled <- tr.Call(ctx, "canceled").Decode(nil) }()
		synctest.Wait() // Peer consumes no bytes from this physical write.
		cancel()
		synctest.Wait()
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation=%v", err)
		}
		for name, result := range map[string]<-chan error{"pending": pending, "stream": stream.Err()} {
			select {
			case err := <-result:
				if !errors.Is(err, transport.ErrConnectionClosed) || !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("%s lost physical failure cause: %v", name, err)
				}
			default:
				t.Fatalf("physical write failure did not release %s", name)
			}
		}
		if err := tr.Call(t.Context(), "late").Decode(nil); !errors.Is(err, transport.ErrConnectionClosed) {
			t.Fatalf("connection reused after physical cancellation failure: %v", err)
		}
	})
}

func TestPhysicalCloseUnblocksReaderWriterAndWriterWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, _ := newPipeConn(t)
		reader, writer, waiter := make(chan error, 1), make(chan error, 1), make(chan error, 1)
		go func() { _, err := conn.ReadMessage(); reader <- err }()
		go func() { _, err := conn.WriteMessage(t.Context(), []byte("writer")); writer <- err }()
		synctest.Wait()
		go func() { _, err := conn.WriteMessage(t.Context(), []byte("waiter")); waiter <- err }()
		synctest.Wait()
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for name, done := range map[string]<-chan error{"reader": reader, "writer": writer, "waiter": waiter} {
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("%s succeeded after physical close", name)
				}
			default:
				t.Fatalf("physical close left %s blocked", name)
			}
		}
	})
}

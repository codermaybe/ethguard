package ipc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
)

func pipeConn(t *testing.T, limit int64) (*Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := newConn(a, limit)
	t.Cleanup(func() { _ = c.Close(); _ = b.Close() })
	return c, b
}

func TestConnectionPreservesPayloadAndWritesOneNewline(t *testing.T) {
	conn, peer := pipeConn(t, 1024)
	payload := make([]byte, 2, 64)
	copy(payload, `{}`)
	full := payload[:cap(payload)]
	for i := len(payload); i < len(full); i++ {
		full[i] = '!'
	}
	saved := bytes.Clone(full)
	got := make(chan []byte, 1)
	go func() {
		b := make([]byte, 3)
		_, err := io.ReadFull(peer, b)
		if err != nil {
			t.Error(err)
		}
		got <- b
	}()
	written, err := conn.WriteMessage(context.Background(), payload)
	if !written || err != nil {
		t.Fatalf("write=(%v,%v)", written, err)
	}
	if !bytes.Equal(<-got, []byte("{}\n")) || !bytes.Equal(full, saved) {
		t.Fatal("write corrupted payload capacity or framing")
	}
}

type injectedConn struct {
	net.Conn
	write       func([]byte) (int, error)
	deadlineErr error
	calls       int
}

func (c *injectedConn) Write(p []byte) (int, error) { c.calls++; return c.write(p) }
func (c *injectedConn) SetWriteDeadline(d time.Time) error {
	if c.deadlineErr != nil {
		return c.deadlineErr
	}
	return c.Conn.SetWriteDeadline(d)
}

func TestConnectionReportsPhysicalWriteProgress(t *testing.T) {
	broken := errors.New("broken socket")
	for _, tc := range []struct {
		name    string
		n       int
		cause   error
		want    bool
		wantErr error
	}{
		{"zero physical", 0, broken, false, broken}, {"partial physical", 1, broken, true, broken},
		{"zero short nil", 0, nil, false, io.ErrShortWrite}, {"partial short nil", 1, nil, true, io.ErrShortWrite},
		{"physical canceled", 0, context.Canceled, false, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, peer := net.Pipe()
			defer func() { _ = peer.Close() }()
			raw := &injectedConn{Conn: base, write: func([]byte) (int, error) { return tc.n, tc.cause }}
			c := newConn(raw, 1024)
			defer func() { _ = c.Close() }()
			written, err := c.WriteMessage(context.Background(), []byte(`{}`))
			if written != tc.want || !errors.Is(err, tc.wantErr) || raw.calls != 1 {
				t.Fatalf("got=(%v,%v) writes=%d", written, err, raw.calls)
			}
			if errors.Is(tc.cause, context.Canceled) && err == context.Canceled { //nolint:errorlint // Identity distinguishes physical wrapping from reusable exact cancellation.
				t.Fatal("physical failure masqueraded as pre-write cancellation")
			}
		})
	}
}

func TestConnectionCancellationBeforeWritePreservesStream(t *testing.T) {
	c, peer := pipeConn(t, 1024)
	c.writeSem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		written, err := c.WriteMessage(ctx, []byte(`{}`))
		if written {
			t.Error("lock wait moved bytes")
		}
		result <- err
	}()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-c.writeSem
	received := make(chan error, 1)
	go func() { b := make([]byte, 3); _, err := io.ReadFull(peer, b); received <- err }()
	if written, err := c.WriteMessage(context.Background(), []byte(`{}`)); !written || err != nil {
		t.Fatalf("reuse=(%v,%v)", written, err)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
}

func TestConnectionWriteTimeoutAndActiveCancellation(t *testing.T) {
	for _, cancelOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "ceiling", true: "cancel-no-deadline"}[cancelOnly], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, _ := pipeConn(t, 1024)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if cancelOnly {
					go func() { time.Sleep(time.Second); cancel() }()
				}
				start := time.Now()
				written, err := c.WriteMessage(ctx, []byte(`{}`))
				want := maxWriteWait
				if cancelOnly {
					want = time.Second
				}
				if written || !errors.Is(err, os.ErrDeadlineExceeded) || time.Since(start) != want {
					t.Fatalf("write=(%v,%v) elapsed=%s", written, err, time.Since(start))
				}
			})
		})
	}
}

func TestConnectionCloseUnblocksReadWriteAndWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := pipeConn(t, 1024)
		results := make(chan error, 3)
		go func() { _, err := c.ReadMessage(); results <- err }()
		go func() { _, err := c.WriteMessage(t.Context(), []byte(`{}`)); results <- err }()
		synctest.Wait()
		go func() { _, err := c.WriteMessage(t.Context(), []byte(`{}`)); results <- err }()
		synctest.Wait()
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := <-results; err == nil {
				t.Fatal("physical Close left operation successful")
			}
		}
	})
}

func TestConnectionReadLimitIsNeutralAndSticky(t *testing.T) {
	c, peer := pipeConn(t, 4)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = peer.Write([]byte(`{"x":1}`)) }()
	_, first := c.ReadMessage()
	if !errors.Is(first, transport.ErrBodyTooLarge) || !errors.Is(first, transport.ErrConnectionClosed) {
		t.Fatalf("limit error=%v", first)
	}
	_, again := c.ReadMessage()
	if !errors.Is(again, transport.ErrBodyTooLarge) {
		t.Fatalf("failure changed=%v", again)
	}
	_ = c.Close()
	<-done
}

type callbackGateConn struct {
	net.Conn
	entered, release chan struct{}
	once             sync.Once
}

func (c *callbackGateConn) SetWriteDeadline(d time.Time) error {
	if !d.After(time.Now()) {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return c.Conn.SetWriteDeadline(d)
}

func TestConnectionJoinsCancellationBeforeUnlocking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		defer func() { _ = b.Close() }()
		raw := &callbackGateConn{Conn: a, entered: make(chan struct{}), release: make(chan struct{})}
		c := newConn(raw, 1024)
		defer func() { _ = c.Close() }()
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() { _, err := c.WriteMessage(ctx, []byte(`{}`)); result <- err }()
		synctest.Wait()
		cancel()
		<-raw.entered
		read := make(chan error, 1)
		go func() { _, err := io.ReadFull(b, make([]byte, 3)); read <- err }()
		if err := <-read; err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("writer unlocked before callback joined: %v", err)
		default:
		}
		close(raw.release)
		if err := <-result; err != nil {
			t.Fatalf("completed write error=%v", err)
		}
		go func() { _, err := io.ReadFull(b, make([]byte, 3)); read <- err }()
		if _, err := c.WriteMessage(t.Context(), []byte(`{}`)); err != nil {
			t.Fatalf("late cancellation poisoned follow-up: %v", err)
		}
		if err := <-read; err != nil {
			t.Fatal(err)
		}
	})
}

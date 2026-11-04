package wss

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
)

func newPipeConn(t *testing.T) (*Conn, net.Conn) {
	t.Helper()

	client, peer := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
	})

	result := &handshakeResult{
		conn: client,
		br:   bufio.NewReader(client),
	}
	return newConn(result, DefaultReadLimit), peer
}

func TestWriteDeadlineUsesFakeTimeAndUnblocksBlockedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const deadline = 5 * time.Second

		conn, _ := newPipeConn(t)
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()

		started := time.Now()
		err := conn.WriteMessage(ctx, []byte(`{"jsonrpc":"2.0"}`))
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("err = %v, want os.ErrDeadlineExceeded", err)
		}
		if elapsed := time.Since(started); elapsed != deadline {
			t.Fatalf("fake elapsed time = %s, want %s", elapsed, deadline)
		}
	})
}

func TestTransportWriteDeadlineAbortsPendingAndRejectsLaterCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const deadline = 5 * time.Second

		conn, peer := newPipeConn(t)
		transportValue := &Transport{
			ws:      conn,
			ids:     rpcwire.NewIDGen(),
			limit:   DefaultReadLimit,
			pending: make(map[string]chan frameResult),
		}
		t.Cleanup(transportValue.Close)
		go transportValue.readLoop()

		firstFrameRead := make(chan struct{})
		go func() {
			// Close on every path: a read failure must surface as its own
			// t.Errorf, not as a synctest deadlock on the receive below.
			defer close(firstFrameRead)
			reader := newFrameReader(bufio.NewReader(peer), serverSide, DefaultReadLimit)
			if _, _, err := reader.readMessage(nil); err != nil {
				t.Errorf("read first request: %v", err)
			}
		}()

		firstResult := make(chan error, 1)
		go func() {
			var result string
			firstResult <- transportValue.CallContext(t.Context(), &result, "eth_pending")
		}()
		<-firstFrameRead

		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()
		started := time.Now()
		var result string
		err := transportValue.CallContext(ctx, &result, "eth_deadline")
		// The caller's deadline timer and the socket write deadline fire at the
		// same fake instant, so the outcome depends on which one the runtime
		// delivers first: whichever it is, CallContext's write-error branch
		// reports ctx.Err() when the timer already ran and the abort cause when
		// it has not. Both are externally legal; the invariants below are not.
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, transport.ErrConnectionClosed) {
			t.Fatalf("deadline call = %v, want context deadline or aborted connection", err)
		}
		if elapsed := time.Since(started); elapsed != deadline {
			t.Fatalf("fake elapsed time = %s, want %s", elapsed, deadline)
		}

		synctest.Wait()
		if err := <-firstResult; !errors.Is(err, transport.ErrConnectionClosed) {
			t.Fatalf("pending call after write deadline = %v, want transport.ErrConnectionClosed", err)
		}
		if n := transportValue.pendingCount(); n != 0 {
			t.Fatalf("abort left %d pending calls", n)
		}
		if err := transportValue.CallContext(t.Context(), &result, "eth_after"); !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("call after write deadline = %v, want ErrTransportClosed", err)
		}
	})
}

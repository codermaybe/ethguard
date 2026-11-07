package wss

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
)

func requestMethod(frame []byte) string {
	var request struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(frame, &request)
	return request.Method
}

func awaitLifecycleSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle event did not complete")
	}
}

func lifecycleResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("pending call did not finish")
		return nil
	}
}

func TestCancellationClearsPendingAndKeepsTheConnection(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{})
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		first := <-peer.requests
		close(received)
		<-release
		peer.respond(first, `"late"`)
		peer.respond(<-peer.requests, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { var value string; result <- tr.CallContext(ctx, &value, "eth_cancel") }()
	awaitLifecycleSignal(t, received)
	cancel()
	if err := lifecycleResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if got := tr.pendingCount(); got != 0 {
		t.Fatalf("pending after cancel = %d", got)
	}
	close(release)
	followCtx, followCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer followCancel()
	var value string
	if err := tr.CallContext(followCtx, &value, "eth_followup"); err != nil || value != "ok" {
		t.Fatalf("late response leaked or socket closed: value=%q err=%v", value, err)
	}
}

func TestCancellationWaitingForWriterDoesNotSendOrAbort(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		if method := requestMethod(request); method != "eth_followup" {
			t.Errorf("canceled writer emitted %q", method)
		}
		peer.respond(request, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The write lock is the capacity-1 channel semaphore now; holding it
	// from the test goroutine parks the queued call on the lock exactly as
	// the old mutex did.
	releaseWriter := tr.ws.writeControlLock()
	result := make(chan error, 1)
	go func() { var value string; result <- tr.CallContext(ctx, &value, "eth_queued") }()
	// The pending entry is installed before waiting on the single-writer lock.
	deadline := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for tr.pendingCount() != 1 {
		select {
		case <-ticker.C:
		case <-deadline.C:
			releaseWriter()
			t.Fatal("writer did not enter pending table")
		}
	}
	cancel()
	releaseWriter()
	if err := lifecycleResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancel: %v", err)
	}
	followCtx, followCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer followCancel()
	var value string
	if err := tr.CallContext(followCtx, &value, "eth_followup"); err != nil || value != "ok" {
		t.Fatalf("healthy socket poisoned: %q %v", value, err)
	}
}

func TestReadFailureAbortsEveryPendingCall(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		<-peer.requests
		<-peer.requests
		_ = peer.conn.Close()
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 2)
	for range 2 {
		go func() { var value string; result <- tr.CallContext(ctx, &value, "eth_pending") }()
	}
	for range 2 {
		if err := lifecycleResult(t, result); !errors.Is(err, transport.ErrConnectionClosed) {
			t.Fatalf("pending: %v", err)
		}
	}
	if tr.pendingCount() != 0 {
		t.Fatal("abort left pending calls")
	}
	var value string
	if err := tr.CallContext(ctx, &value, "eth_after"); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("dead socket reused: %v", err)
	}
}

func TestReadLimitFailsPendingCallsAndClosesTheTransport(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.respond(request, `"`+strings.Repeat("a", 256)+`"`)
	})
	tr := dialTestTransport(t, peerURL, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var value string
	if err := tr.CallContext(ctx, &value, "eth_large"); !errors.Is(err, transport.ErrBodyTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	if tr.pendingCount() != 0 {
		t.Fatal("read limit left pending calls")
	}
	if err := tr.CallContext(ctx, &value, "eth_after"); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("read-limited socket reused: %v", err)
	}
}

type failingWriteConn struct {
	net.Conn
	fail atomic.Bool
}

func (c *failingWriteConn) Write(body []byte) (int, error) {
	if c.fail.Load() {
		return 0, errors.New("injected frame write failure")
	}
	return c.Conn.Write(body)
}

func TestFrameWriteFailureAbortsOtherPendingCalls(t *testing.T) {
	received := make(chan struct{})
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		<-peer.requests
		close(received)
		for range peer.requests {
		}
	})
	var network *failingWriteConn
	result, err := dialWebSocket(context.Background(), peerURL, dialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	network = &failingWriteConn{Conn: result.conn}
	result.conn = network
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tr := &Transport{ws: newConn(result, DefaultReadLimit), ids: rpcwire.NewIDGen(), limit: DefaultReadLimit, pending: make(map[string]chan frameResult)}
	t.Cleanup(tr.Close)
	go tr.readLoop()
	result1 := make(chan error, 1)
	go func() { var value string; result1 <- tr.CallContext(ctx, &value, "eth_pending") }()
	awaitLifecycleSignal(t, received)
	network.fail.Store(true)
	var value string
	if err := tr.CallContext(ctx, &value, "eth_write"); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("frame write: %v", err)
	}
	if err := lifecycleResult(t, result1); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("other pending survived broken frame: %v", err)
	}
	if err := tr.CallContext(ctx, &value, "eth_after"); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("failed writer reused: %v", err)
	}
}

func TestCloseDrainsPendingWithoutAcceptingNewCalls(t *testing.T) {
	for _, cancelPending := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "cancel"}[cancelPending], func(t *testing.T) {
			received := make(chan struct{})
			release := make(chan struct{})
			peerDone := make(chan struct{})
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				defer close(peerDone)
				request := <-peer.requests
				close(received)
				<-release
				if !cancelPending {
					peer.respond(request, `"drained"`)
				}
				for range peer.requests {
				}
			})
			tr := dialTestTransport(t, peerURL, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				var value string
				err := tr.CallContext(ctx, &value, "eth_pending")
				if err == nil && value != "drained" {
					err = errors.New("pending response changed")
				}
				result <- err
			}()
			awaitLifecycleSignal(t, received)
			tr.Close()
			tr.Close()
			var ignored string
			if err := tr.CallContext(ctx, &ignored, "eth_new"); !errors.Is(err, ErrTransportClosed) {
				t.Fatalf("Close accepted new call: %v", err)
			}
			if tr.pendingCount() != 1 {
				t.Fatal("Close aborted existing pending")
			}
			if cancelPending {
				cancel()
			}
			close(release)
			err := lifecycleResult(t, result)
			if cancelPending && !errors.Is(err, context.Canceled) {
				t.Fatalf("pending cancellation: %v", err)
			}
			if !cancelPending && err != nil {
				t.Fatalf("Close discarded response: %v", err)
			}
			awaitLifecycleSignal(t, peerDone)
			if tr.pendingCount() != 0 {
				t.Fatal("Close left pending calls")
			}
		})
	}
}

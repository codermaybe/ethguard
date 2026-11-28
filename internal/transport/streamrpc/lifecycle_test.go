package streamrpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
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

var errInjectedFrameWrite = errors.New("injected frame write failure")

type gatedCloseConn struct {
	MessageConn
	entered, release chan struct{}
}

func (c *gatedCloseConn) Close() error { close(c.entered); <-c.release; return c.MessageConn.Close() }
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
	go func() { var value string; result <- tr.Call(ctx, "eth_cancel").Decode(&value) }()
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
	if err := tr.Call(followCtx, "eth_followup").Decode(&value); err != nil || value != "ok" {
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
	releaseWriter := tr.conn.(*scriptedConn).holdWriter()
	result := make(chan error, 1)
	go func() { var value string; result <- tr.Call(ctx, "eth_queued").Decode(&value) }()
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
	if err := tr.Call(followCtx, "eth_followup").Decode(&value); err != nil || value != "ok" {
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
		go func() { var value string; result <- tr.Call(ctx, "eth_pending").Decode(&value) }()
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
	if err := tr.Call(ctx, "eth_after").Decode(&value); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("dead socket reused: %v", err)
	}
}

func TestAbortOwnsFailureWhileSocketCloseIsBlocked(t *testing.T) {
	conn := newScriptedPeer(t, func(peer *scriptedPeer) {
		for range peer.requests {
		}
	})
	gate := &gatedCloseConn{MessageConn: conn, entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(gate.release) })
	defer release()
	pending := make(chan frameResult, 1)
	tr := &Transport{
		conn: gate, ids: rpcwire.NewIDGen(),
		pending: map[string]chan frameResult{"pending": pending},
	}
	t.Cleanup(tr.Close)
	stream, err := tr.OpenNotificationStream(t.Context(), "source", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstCause := errors.Join(errInjectedFrameWrite, transport.ErrConnectionClosed)
	firstDone := make(chan struct{})
	go func() {
		tr.abort(firstCause)
		close(firstDone)
	}()
	awaitLifecycleSignal(t, gate.entered)

	// A reader awakened by the first failure observes a closed transport.
	// Its consequential failure must leave the first abort in sole charge
	// of pending calls and streams, even before socket Close returns.
	secondDone := make(chan struct{})
	go func() {
		tr.abort(tr.classifyReadError(net.ErrClosed))
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("secondary abort joined the in-progress failure fan-out")
	}
	release()
	awaitLifecycleSignal(t, firstDone)
	for name, cause := range map[string]error{"pending": (<-pending).err, "subscription": lifecycleResult(t, stream.Err())} {
		if !errors.Is(cause, errInjectedFrameWrite) || !errors.Is(cause, transport.ErrConnectionClosed) {
			t.Fatalf("%s lost the initiating failure: %v", name, cause)
		}
	}
	if err := tr.Call(t.Context(), "eth_after").Decode(nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("aborted connection accepted a new call: %v", err)
	}
}

func TestReadFailureAbortsPendingDuringGracefulClose(t *testing.T) {
	received := make(chan struct{})
	failPeer := make(chan struct{})
	releasePeer := sync.OnceFunc(func() { close(failPeer) })
	defer releasePeer()
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		<-peer.requests
		close(received)
		<-failPeer
		_ = peer.conn.Close()
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	pending := make(chan error, 1)
	go func() { pending <- tr.Call(ctx, "eth_pending").Decode(nil) }()
	awaitLifecycleSignal(t, received)
	tr.Close()
	if tr.pendingCount() != 1 {
		t.Fatal("graceful Close discarded the in-flight call")
	}
	releasePeer()
	if err := lifecycleResult(t, pending); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("read failure during graceful Close did not abort pending: %v", err)
	}
	if tr.pendingCount() != 0 {
		t.Fatal("read failure left pending calls during graceful Close")
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
				err := tr.Call(ctx, "eth_pending").Decode(&value)
				if err == nil && value != "drained" {
					err = errors.New("pending response changed")
				}
				result <- err
			}()
			awaitLifecycleSignal(t, received)
			tr.Close()
			tr.Close()
			var ignored string
			if err := tr.Call(ctx, "eth_new").Decode(&ignored); !errors.Is(err, ErrTransportClosed) {
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

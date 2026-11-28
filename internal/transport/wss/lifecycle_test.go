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

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/streamrpc"
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

func TestDialContextCancellationKeepsEstablishedConnection(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request, ok := <-peer.requests
		if !ok {
			return
		}
		peer.respond(request, `"0x64"`)
	})
	dialCtx, cancelDial := context.WithCancel(context.Background())
	defer cancelDial()
	tr, err := NewDialer().DialContext(dialCtx, peerURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(tr.Close)
	cancelDial()

	callCtx, cancelCall := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCall()
	var value string
	if err := tr.Call(callCtx, "eth_getBalance", "0x1", "pending").Decode(&value); err != nil || value != "0x64" {
		t.Fatalf("call after dial context canceled: value=%q err=%v", value, err)
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
	if err := tr.Call(ctx, "eth_large").Decode(&value); !errors.Is(err, transport.ErrBodyTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	if err := tr.Call(ctx, "eth_after").Decode(&value); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("read-limited socket reused: %v", err)
	}
}

var errInjectedFrameWrite = errors.New("injected frame write failure")

type failingWriteConn struct {
	net.Conn
	fail atomic.Bool
}

func (c *failingWriteConn) Write(body []byte) (int, error) {
	if c.fail.Load() {
		return 0, errInjectedFrameWrite
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
	tr := &Transport{Transport: streamrpc.New(newConn(result, DefaultReadLimit), streamrpc.Options{})}
	t.Cleanup(tr.Close)
	stream, err := tr.OpenNotificationStream(ctx, "source", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	result1 := make(chan error, 1)
	go func() { var value string; result1 <- tr.Call(ctx, "eth_pending").Decode(&value) }()
	awaitLifecycleSignal(t, received)
	network.fail.Store(true)
	var value string
	if err := tr.Call(ctx, "eth_write").Decode(&value); !errors.Is(err, transport.ErrConnectionClosed) || !errors.Is(err, errInjectedFrameWrite) {
		t.Fatalf("frame write lost its causes: %v", err)
	}
	if err := lifecycleResult(t, result1); !errors.Is(err, transport.ErrConnectionClosed) || !errors.Is(err, errInjectedFrameWrite) {
		t.Fatalf("other pending lost its causes: %v", err)
	}
	if err := lifecycleResult(t, stream.Err()); !errors.Is(err, transport.ErrConnectionClosed) || !errors.Is(err, errInjectedFrameWrite) {
		t.Fatalf("subscription lost its causes: %v", err)
	}
	if err := tr.Call(ctx, "eth_after").Decode(&value); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("failed writer reused: %v", err)
	}
}

package ipc_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/ipc"
	"github.com/codermaybe/ethguard/rpc"
)

type ipcRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type ipcRPCPeer struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
}

func (p *ipcRPCPeer) request() (ipcRequest, bool) {
	p.t.Helper()
	// The client must emit a newline after every complete request. Parsing
	// by line here deliberately tests that outbound compatibility promise.
	line, err := p.reader.ReadBytes('\n')
	if err != nil {
		p.t.Errorf("read newline-terminated IPC request: %v", err)
		return ipcRequest{}, false
	}
	var request ipcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		p.t.Errorf("decode IPC request: %v", err)
		return ipcRequest{}, false
	}
	return request, true
}

func (p *ipcRPCPeer) write(body []byte) bool {
	p.t.Helper()
	if _, err := p.conn.Write(body); err != nil {
		p.t.Errorf("IPC peer write: %v", err)
		return false
	}
	return true
}

func (p *ipcRPCPeer) respond(request ipcRequest, result string) bool {
	return p.write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result)))
}

func serveIPCContractPeer(t *testing.T, listener net.Listener, script func(*ipcRPCPeer)) {
	t.Helper()
	var mu sync.Mutex
	var accepted net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		accepted = conn
		mu.Unlock()
		defer func() { _ = conn.Close() }()
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Errorf("IPC peer deadline: %v", err)
			return
		}
		script(&ipcRPCPeer{t: t, conn: conn, reader: bufio.NewReader(conn)})
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		if accepted != nil {
			_ = accepted.Close()
		}
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("IPC peer did not stop")
		}
	})
}

func ipcReceive[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("IPC operation did not settle")
		var zero T
		return zero
	}
}

func dialIPCContract(t *testing.T, target string) transport.Transport {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tr, err := ipc.NewDialer().DialContext(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.Close)
	return tr
}

func runIPCConcurrentResponsesAndProtocolPresence(t *testing.T, listener net.Listener, target string) {
	const concurrent = 8
	serveIPCContractPeer(t, listener, func(peer *ipcRPCPeer) {
		requests := make([]ipcRequest, 0, concurrent)
		for range concurrent {
			request, ok := peer.request()
			if !ok {
				return
			}
			requests = append(requests, request)
		}
		var adjacent bytes.Buffer
		for i := len(requests) - 1; i >= 0; i-- {
			request := requests[i]
			if request.Method != "echo" || len(request.Params) != 1 {
				t.Errorf("unexpected concurrent request: %+v", request)
				return
			}
			body := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, request.Params[0]))
			if err := json.Indent(&adjacent, body, "", "  "); err != nil {
				t.Error(err)
				return
			}
		}
		// Every object is multiline; successive objects are adjacent }{ with
		// no newline or whitespace delimiter at all.
		if !peer.write(adjacent.Bytes()) {
			return
		}
		for _, data := range []string{"", `,"data":null`, `,"data":{"reason":"bad"}`} {
			request, ok := peer.request()
			if !ok || !peer.write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":3,"message":"reverted"%s}}`, request.ID, data))) {
				return
			}
		}
		request, ok := peer.request()
		if !ok || request.Method != "eth_chainId" || !peer.respond(request, `"0x1"`) {
			return
		}
		_, _ = io.Copy(io.Discard, peer.reader)
	})
	tr := dialIPCContract(t, target)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var value int
			if err := tr.Call(ctx, "echo", i).Decode(&value); err != nil || value != i {
				t.Errorf("out-of-order response: value=%d want=%d err=%v", value, i, err)
			}
		}()
	}
	wg.Wait()
	for _, want := range []string{"", "null", `{"reason":"bad"}`} {
		err := tr.Call(ctx, "eth_call").Decode(nil)
		var protocol rpc.ProtocolError
		var presence rpc.ErrorDataPresence
		if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 || !errors.As(err, &presence) {
			t.Fatalf("protocol error capabilities: %v", err)
		}
		data, present := presence.ErrorDataJSON()
		if present != (want != "") || string(data) != want {
			t.Fatalf("error data=%s present=%v, want %q", data, present, want)
		}
	}
	if id, err := tr.ChainID(ctx); err != nil || id == nil || id.Uint64() != 1 {
		t.Fatalf("connection after protocol errors: chain=%v err=%v", id, err)
	}
}

func runIPCCancelAndGracefulClose(t *testing.T, listener net.Listener, target string) {
	canceledSeen, sendLate := make(chan struct{}), make(chan struct{})
	drainingSeen, finishDraining := make(chan struct{}), make(chan struct{})
	peerClosed := make(chan struct{})
	releaseLate := sync.OnceFunc(func() { close(sendLate) })
	releaseDrain := sync.OnceFunc(func() { close(finishDraining) })
	t.Cleanup(releaseLate)
	t.Cleanup(releaseDrain)
	serveIPCContractPeer(t, listener, func(peer *ipcRPCPeer) {
		defer close(peerClosed)
		first, ok := peer.request()
		if !ok {
			return
		}
		close(canceledSeen)
		<-sendLate
		if !peer.respond(first, `"late"`) {
			return
		}
		second, ok := peer.request()
		if !ok || !peer.respond(second, `"kept"`) {
			return
		}
		last, ok := peer.request()
		if !ok {
			return
		}
		close(drainingSeen)
		<-finishDraining
		if !peer.respond(last, `"drained"`) {
			return
		}
		_, _ = io.Copy(io.Discard, peer.reader)
	})
	tr := dialIPCContract(t, target)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- tr.Call(ctx, "canceled").Decode(nil) }()
	ipcReceive(t, canceledSeen)
	cancel()
	if err := ipcReceive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("unary cancellation=%v", err)
	}
	releaseLate()
	followCtx, followCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer followCancel()
	var value string
	if err := tr.Call(followCtx, "after").Decode(&value); err != nil || value != "kept" {
		t.Fatalf("late response poisoned subsequent call: value=%q err=%v", value, err)
	}
	go func() { done <- tr.Call(followCtx, "draining").Decode(&value) }()
	ipcReceive(t, drainingSeen)
	tr.Close()
	if err := tr.Call(followCtx, "new").Decode(nil); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("graceful close accepted a new call: %v", err)
	}
	releaseDrain()
	if err := ipcReceive(t, done); err != nil || value != "drained" {
		t.Fatalf("graceful close discarded pending result: %q %v", value, err)
	}
	ipcReceive(t, peerClosed)
	tr.Close()
}

func runIPCPrebindSubscriptionAndUnarySharing(t *testing.T, listener net.Listener, target string) {
	serveIPCContractPeer(t, listener, func(peer *ipcRPCPeer) {
		request, ok := peer.request()
		if !ok {
			return
		}
		if request.Method != "eth_subscribe" || len(request.Params) != 1 || string(request.Params[0]) != `"newHeads"` {
			t.Errorf("IPC subscription registration=%+v", request)
			return
		}
		// Notification intentionally precedes the setup answer in one write.
		body := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"ipc-sub","result":{"number":"0x1"}}}{"jsonrpc":"2.0","id":%s,"result":"ipc-sub"}`, request.ID))
		if !peer.write(body) {
			return
		}
		request, ok = peer.request()
		if !ok || request.Method != "eth_chainId" || !peer.respond(request, `"0x1"`) {
			return
		}
		request, ok = peer.request()
		if !ok {
			return
		}
		if request.Method != "eth_unsubscribe" || len(request.Params) != 1 || string(request.Params[0]) != `"ipc-sub"` {
			t.Errorf("IPC unsubscribe=%+v", request)
			return
		}
		if !peer.respond(request, `true`) {
			return
		}
		request, ok = peer.request()
		if !ok || !peer.respond(request, `"0x1"`) {
			return
		}
		_, _ = io.Copy(io.Discard, peer.reader)
	})
	tr := dialIPCContract(t, target)
	capability, ok := tr.(transport.BoundedSubscriber)
	if !ok {
		t.Fatal("IPC transport omitted bounded subscription capability")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := capability.SubscribeBounded(ctx, transport.NotificationPolicy{Messages: 2, Bytes: 4096, MaxMessageBytes: 1024}, "newHeads")
	if err != nil {
		t.Fatal(err)
	}
	event := ipcReceive(t, sub.Events())
	if event.SubscriptionID != "ipc-sub" || string(event.Result) != `{"number":"0x1"}` || event.Release == nil {
		t.Fatalf("pre-bind event=%+v", event)
	}
	event.Release()
	if id, err := tr.ChainID(ctx); err != nil || id == nil || id.Uint64() != 1 {
		t.Fatalf("unary sharing subscription socket: %v %v", id, err)
	}
	sub.Unsubscribe()
	sub.Unsubscribe()
	if err := ipcReceive(t, sub.Err()); err != nil {
		t.Fatalf("normal unsubscribe=%v", err)
	}
	if _, err := tr.ChainID(ctx); err != nil {
		t.Fatalf("unsubscribe closed the shared unary socket: %v", err)
	}
}

package streamrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/streamrpc"
)

type seamRead struct {
	body []byte
	err  error
}

type seamRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// This peer controls the MessageConn boundary, without depending on the
// transport's pending maps, queue representation, or goroutine scheduling.
type seamConn struct {
	reads      chan seamRead
	requests   chan seamRequest
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int32
	onWrite    func(context.Context, seamRequest) (bool, error)
	onClose    func()
	closedRead error
}

func newSeamConn(t *testing.T) *seamConn {
	t.Helper()
	c := &seamConn{reads: make(chan seamRead, 32), requests: make(chan seamRequest, 32), closed: make(chan struct{}), closedRead: errors.New("physical read awakened by close")}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (c *seamConn) ReadMessage() ([]byte, error) {
	select {
	case <-c.closed:
		return nil, c.closedRead
	case event := <-c.reads:
		return event.body, event.err
	}
}

func (c *seamConn) WriteMessage(ctx context.Context, body []byte) (bool, error) {
	var request seamRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return false, err
	}
	c.requests <- request
	return c.onWrite(ctx, request)
}

func (c *seamConn) Close() error {
	c.closeCalls.Add(1)
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.onClose != nil {
			c.onClose()
		}
	})
	return nil
}

func (c *seamConn) respond(request seamRequest, result string) {
	c.reads <- seamRead{body: []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result))}
}

func (c *seamConn) notify(id, result string) {
	c.reads <- seamRead{body: []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, id, result))}
}

func seamReceive[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("controlled transport operation did not settle")
		var zero T
		return zero
	}
}

func seamCall(t *testing.T, tr *streamrpc.Transport, method string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- tr.Call(t.Context(), method).Decode(nil) }()
	return done
}

func assertSeamHealthy(t *testing.T, tr *streamrpc.Transport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var result string
	if err := tr.Call(ctx, "followup").Decode(&result); err != nil || result != "ok" {
		t.Fatalf("healthy followup: result=%q err=%v", result, err)
	}
}

func TestMessageWriteOutcomeControlsConnectionHealth(t *testing.T) {
	physicalFailure := errors.New("physical writer failed")
	for _, tc := range []struct {
		name    string
		written bool
		cause   error
		cancel  bool
		healthy bool
	}{
		{name: "complete", written: true, healthy: true},
		{name: "zero bytes canceled", cause: context.Canceled, cancel: true, healthy: true},
		{name: "partial write canceled", written: true, cause: context.Canceled, cancel: true},
		{name: "partial write physical error", written: true, cause: physicalFailure},
		{name: "zero bytes physical error", cause: physicalFailure},
		{name: "physical error while context canceled", cause: physicalFailure, cancel: true},
		{name: "joined physical and context errors", cause: errors.Join(context.Canceled, physicalFailure), cancel: true},
		{name: "context sentinel without canceled caller", cause: context.Canceled},
		{name: "false nil contract violation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newSeamConn(t)
			entered := make(chan struct{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			conn.onWrite = func(ctx context.Context, request seamRequest) (bool, error) {
				switch request.Method {
				case "pending":
					return true, nil
				case "outcome":
					close(entered)
					if tc.cancel {
						<-ctx.Done()
					}
					if tc.written && tc.cause == nil {
						conn.respond(request, `"ok"`)
					}
					return tc.written, tc.cause
				default:
					conn.respond(request, `"ok"`)
					return true, nil
				}
			}
			tr := streamrpc.New(conn, streamrpc.Options{})
			t.Cleanup(tr.Close)
			stream, err := tr.OpenNotificationStream(t.Context(), "source", 1, 2)
			if err != nil {
				t.Fatal(err)
			}
			pending := seamCall(t, tr, "pending")
			request := seamReceive(t, conn.requests)
			if request.Method != "pending" {
				t.Fatal("pending call was not installed before the outcome")
			}
			done := make(chan error, 1)
			go func() { done <- tr.Call(ctx, "outcome").Decode(nil) }()
			seamReceive(t, entered)
			if tc.cancel {
				cancel()
			}
			err = seamReceive(t, done)
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation lost: %v", err)
			}
			if tc.healthy {
				if !tc.cancel && err != nil {
					t.Fatal(err)
				}
				if conn.closeCalls.Load() != 0 {
					t.Fatal("healthy write outcome closed the connection")
				}
				conn.respond(request, `"kept"`)
				if err := seamReceive(t, pending); err != nil {
					t.Fatalf("unrelated pending was poisoned: %v", err)
				}
				conn.notify("existing", `"kept"`)
				n := seamReceive(t, stream.Events())
				if string(n.Result) != `"kept"` {
					t.Fatal("unrelated subscription stopped delivering")
				}
				n.Release()
				assertSeamHealthy(t, tr)
			} else {
				if !tc.cancel && !errors.Is(err, transport.ErrConnectionClosed) {
					t.Fatalf("fatal write outcome returned %v", err)
				}
				for name, cause := range map[string]error{"pending": seamReceive(t, pending), "stream": seamReceive(t, stream.Err())} {
					if !errors.Is(cause, transport.ErrConnectionClosed) || (tc.cause != nil && !errors.Is(cause, tc.cause)) {
						t.Fatalf("%s lost write failure classification: %v", name, cause)
					}
				}
				if conn.closeCalls.Load() != 1 {
					t.Fatalf("physical close count=%d, want one", conn.closeCalls.Load())
				}
				if err := tr.Call(t.Context(), "followup").Decode(nil); !errors.Is(err, transport.ErrConnectionClosed) {
					t.Fatalf("failed connection accepted followup: %v", err)
				}
			}
		})
	}
}

func TestWriteFailureOwnsPendingCauseEvenDuringGracefulClosing(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		t.Run(fmt.Sprintf("closing=%v", graceful), func(t *testing.T) {
			conn := newSeamConn(t)
			writerEntered, failWriter := make(chan struct{}), make(chan struct{})
			closeEntered, finishClose := make(chan struct{}), make(chan struct{})
			releaseWriter := sync.OnceFunc(func() { close(failWriter) })
			releaseClose := sync.OnceFunc(func() { close(finishClose) })
			t.Cleanup(releaseWriter)
			t.Cleanup(releaseClose)
			firstCause := errors.New("original write failure")
			conn.onClose = func() { close(closeEntered); <-finishClose }
			conn.onWrite = func(ctx context.Context, request seamRequest) (bool, error) {
				if request.Method == "failing" {
					close(writerEntered)
					select {
					case <-failWriter:
					case <-ctx.Done():
					}
					return true, firstCause
				}
				return true, nil
			}
			tr := streamrpc.New(conn, streamrpc.Options{})
			t.Cleanup(func() { releaseWriter(); releaseClose(); tr.Close() })
			stream, err := tr.OpenNotificationStream(t.Context(), "source", 1, 2)
			if err != nil {
				t.Fatal(err)
			}
			pending := seamCall(t, tr, "pending")
			seamReceive(t, conn.requests)
			failing := seamCall(t, tr, "failing")
			seamReceive(t, writerEntered)
			if graceful {
				tr.Close()
				if conn.closeCalls.Load() != 0 {
					t.Fatal("graceful close aborted in-flight calls")
				}
			}
			releaseWriter()
			seamReceive(t, closeEntered)
			// Physical close has awakened ReadMessage with a different failure,
			// while the initiating write's Close call still cannot return.
			if err := tr.Call(t.Context(), "late").Decode(nil); !errors.Is(err, transport.ErrConnectionClosed) {
				t.Fatalf("termination was not published before physical close: %v", err)
			}
			releaseClose()
			causes := []error{seamReceive(t, pending), seamReceive(t, failing)}
			if !graceful {
				causes = append(causes, seamReceive(t, stream.Err()))
			}
			for _, cause := range causes {
				if !errors.Is(cause, firstCause) || !errors.Is(cause, transport.ErrConnectionClosed) || errors.Is(cause, conn.closedRead) {
					t.Fatalf("consequential reader failure replaced first owner: %v", cause)
				}
			}
			if conn.closeCalls.Load() != 1 {
				t.Fatalf("physical Close called %d times", conn.closeCalls.Load())
			}
		})
	}
}

func TestCanceledSetupUsesAnAlreadyAssociatedSubscriptionAnswer(t *testing.T) {
	conn := newSeamConn(t)
	subscribeWritten, finishWrite := make(chan struct{}), make(chan struct{})
	releaseWrite := sync.OnceFunc(func() { close(finishWrite) })
	t.Cleanup(releaseWrite)
	var retired atomic.Int32
	conn.onWrite = func(ctx context.Context, request seamRequest) (bool, error) {
		switch request.Method {
		case "eth_subscribe":
			conn.notify("created", `"early"`)
			conn.respond(request, `"created"`)
			close(subscribeWritten)
			select {
			case <-finishWrite:
			case <-conn.closed:
			}
			return true, nil
		case "eth_unsubscribe":
			if len(request.Params) != 1 || string(request.Params[0]) != `"created"` {
				return false, errors.New("retired the wrong subscription")
			}
			retired.Add(1)
			conn.respond(request, `true`)
		default:
			conn.respond(request, `"ok"`)
		}
		return true, nil
	}
	tr := streamrpc.New(conn, streamrpc.Options{})
	t.Cleanup(func() { releaseWrite(); tr.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		sub, err := tr.Subscribe(ctx, "newHeads")
		if sub != nil {
			sub.Unsubscribe()
		}
		done <- err
	}()
	seamReceive(t, subscribeWritten)
	// The later unary response is a read-loop barrier: the subscription
	// answer was associated while its physical writer was still returning.
	assertSeamHealthy(t, tr)
	cancel()
	releaseWrite()
	if err := seamReceive(t, done); !errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("known subscription setup cleanup=%v", err)
	}
	if retired.Load() != 1 || conn.closeCalls.Load() != 0 {
		t.Fatalf("retired=%d physical closes=%d", retired.Load(), conn.closeCalls.Load())
	}
	assertSeamHealthy(t, tr)
}

func TestCanceledWrittenSetupWithoutAnAnswerRetiresTheConnection(t *testing.T) {
	conn := newSeamConn(t)
	conn.onWrite = func(_ context.Context, request seamRequest) (bool, error) {
		if request.Method == "eth_subscribe" {
			conn.notify("unknown", `"early"`)
		} else if request.Method == "followup" {
			conn.respond(request, `"ok"`)
		}
		return true, nil
	}
	tr := streamrpc.New(conn, streamrpc.Options{})
	t.Cleanup(tr.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := tr.Subscribe(ctx, "newHeads"); done <- err }()
	seamReceive(t, conn.requests)
	assertSeamHealthy(t, tr) // Ensures the pre-bind notification was consumed.
	pending := seamCall(t, tr, "pending")
	for seamReceive(t, conn.requests).Method != "pending" {
	}
	cancel()
	if err := seamReceive(t, done); !errors.Is(err, context.Canceled) || !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("uncertain setup did not retire socket: %v", err)
	}
	if err := seamReceive(t, pending); !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("uncertain setup left unrelated call alive: %v", err)
	}
	if conn.closeCalls.Load() != 1 {
		t.Fatalf("uncertain setup closed %d times", conn.closeCalls.Load())
	}
}

func TestPrebindNotificationLeasesHaveOneReleaseOwner(t *testing.T) {
	for _, terminal := range []string{"unsubscribe", "owner close", "producer failure"} {
		t.Run(terminal, func(t *testing.T) {
			conn := newSeamConn(t)
			conn.onWrite = func(_ context.Context, request seamRequest) (bool, error) {
				switch request.Method {
				case "eth_subscribe":
					for _, result := range []string{`"one"`, `"two"`, `"three"`} {
						conn.notify("early", result)
					}
					conn.respond(request, `"early"`)
				case "eth_unsubscribe":
					conn.respond(request, `true`)
				default:
					conn.respond(request, `"ok"`)
				}
				return true, nil
			}
			tr := streamrpc.New(conn, streamrpc.Options{})
			t.Cleanup(tr.Close)
			var leasesMu sync.Mutex
			var releases []*atomic.Int32
			policy := transport.NotificationPolicy{Messages: 3, Bytes: 8192, MaxMessageBytes: 1024, Reserve: func(uint64) (func(), error) {
				calls := new(atomic.Int32)
				leasesMu.Lock()
				releases = append(releases, calls)
				leasesMu.Unlock()
				// Deliberately non-idempotent: the producer must provide Once.
				return func() { calls.Add(1) }, nil
			}}
			sub, err := tr.SubscribeBounded(t.Context(), policy, "newHeads")
			if err != nil {
				t.Fatal(err)
			}
			first := seamReceive(t, sub.Events())
			if string(first.Result) != `"one"` {
				t.Fatalf("pre-bind FIFO starts with %s", first.Result)
			}
			switch terminal {
			case "unsubscribe":
				sub.Unsubscribe()
				sub.Unsubscribe()
			case "owner close":
				tr.Close()
				tr.Close()
			case "producer failure":
				conn.reads <- seamRead{body: []byte(`{"jsonrpc":"1.0","method":"eth_subscription","params":{"subscription":"early","result":null}}`)}
				assertSeamHealthy(t, tr)
				for _, want := range []string{`"two"`, `"three"`} {
					n := seamReceive(t, sub.Events())
					if string(n.Result) != want {
						t.Fatalf("failure broke FIFO: got=%s want=%s", n.Result, want)
					}
					n.Release()
					n.Release()
				}
				if err := seamReceive(t, sub.Err()); err == nil {
					t.Fatal("producer failure lost terminal error after FIFO drain")
				}
				sub.Unsubscribe()
			}
			leasesMu.Lock()
			defer leasesMu.Unlock()
			if len(releases) != 3 {
				t.Fatalf("pre-bind transfer reserved %d leases", len(releases))
			}
			if releases[0].Load() != 0 {
				t.Fatal("producer reclaimed the already handed-off lease")
			}
			first.Release()
			first.Release()
			for i, calls := range releases {
				if calls.Load() != 1 {
					t.Fatalf("lease %d released %d times", i, calls.Load())
				}
			}
		})
	}
}

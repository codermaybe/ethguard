package wss

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
)

// gatedWriteConn stalls inside the first Write until the test opens the
// gate, mimicking a peer that completed the handshake and then stopped
// draining the socket (TCP zero window).
type gatedWriteConn struct {
	net.Conn
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func (c *gatedWriteConn) Write(b []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	<-c.gate
	return c.Conn.Write(b)
}

// TestDeadlineCallerSurvivesStalledWriter pins the write-lock contract
// against the stalled-writer wedge: a no-deadline call stuck writing on a
// connection the peer stopped draining holds the write lock, but a caller
// with its own deadline must return at its deadline instead of queueing
// forever behind the stalled holder, and once the stall clears the original
// call completes and the transport stays usable.
func TestDeadlineCallerSurvivesStalledWriter(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		// Two requests keep the raw server's handler (and connection)
		// alive for the follow-up: the stalled call's frame after the
		// gate opens, then the post-escape follow-up.
		request := <-peer.requests
		peer.respond(request, `"ok"`)
		followup := <-peer.requests
		peer.respond(followup, `"ok"`)
	})
	result, err := dialWebSocket(context.Background(), peerURL, dialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gated := &gatedWriteConn{
		Conn:    result.conn,
		entered: make(chan struct{}),
		gate:    make(chan struct{}),
	}
	result.conn = gated
	tr := &Transport{
		ws:      newConn(result, DefaultReadLimit),
		ids:     rpcwire.NewIDGen(),
		limit:   DefaultReadLimit,
		pending: make(map[string]chan frameResult),
	}
	t.Cleanup(tr.Close)
	go tr.readLoop()

	stalled := make(chan error, 1)
	go func() {
		var value string
		stalled <- tr.CallContext(context.Background(), &value, "eth_stalled")
	}()
	select {
	case <-gated.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("stalled writer never reached the gate")
	}

	// The stalled writer holds the write lock; the deadline caller must
	// escape at its own deadline, not behind the stalled holder.
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	var value string
	err = tr.CallContext(deadlineCtx, &value, "eth_deadline")
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline caller error = %v, want DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("deadline caller returned after %s, wedged behind the stalled writer", elapsed)
	}

	// Releasing the stall lets the original call complete: the transport
	// itself is not poisoned by the escape.
	close(gated.gate)
	select {
	case err := <-stalled:
		if err != nil {
			t.Fatalf("stalled call did not complete after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stalled call never completed after release")
	}
	followCtx, followCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer followCancel()
	if err := tr.CallContext(followCtx, &value, "eth_followup"); err != nil || value != "ok" {
		t.Fatalf("healthy socket poisoned by the stalled-writer escape: %q %v", value, err)
	}
}

package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
)

// This file proves the native HTTP adapter's pool-observable outcomes match
// the audited class A semantics, and pins the one deliberate health
// difference: a degenerate JSON-RPC error member is a failure under the
// native contract where the Geth baseline recorded a healthy usable answer.

type poolEchoServer struct {
	srv      *httptest.Server
	listener *poolCountingListener

	mu       sync.Mutex
	requests []poolCapturedRequest
}

type poolCapturedRequest struct {
	Header nethttp.Header
	Body   []byte
}

type poolCountingListener struct {
	net.Listener
	mu sync.Mutex
	n  int
}

func (l *poolCountingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.n++
		l.mu.Unlock()
	}
	return c, err
}

// newPoolEchoServerHandler starts the loopback server with an explicit
// handler, configured before Start so nothing is written on a live server.
func newPoolEchoServerHandler(t *testing.T, handler nethttp.Handler) *poolEchoServer {
	t.Helper()
	s := &poolEchoServer{}
	s.srv = httptest.NewUnstartedServer(handler)
	s.listener = &poolCountingListener{Listener: s.srv.Listener}
	s.srv.Listener = s.listener
	s.srv.Start()
	t.Cleanup(s.srv.Close)
	return s
}

// newPoolEchoServer starts a loopback JSON-RPC server that echoes the request
// id and answers with the nth body from respond.
func newPoolEchoServer(t *testing.T, respond func(n int, req poolCapturedRequest) []byte) *poolEchoServer {
	t.Helper()
	var s *poolEchoServer
	s = newPoolEchoServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, _ := io.ReadAll(r.Body)
		req := poolCapturedRequest{Header: r.Header.Clone(), Body: body}
		s.mu.Lock()
		n := len(s.requests)
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal(req.Body, &parsed)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,%s}`, parsed.ID, respond(n, req))
	}))
	return s
}

// TestNativeAdapterPoolHealthyAnswer runs a success and a revert (a healthy
// JSON-RPC error) through the pool with the native adapter.
func TestNativeAdapterPoolHealthyAnswer(t *testing.T) {
	srv := newPoolEchoServer(t, func(n int, req poolCapturedRequest) []byte {
		if n%2 == 0 {
			return []byte(`"result":"0x2a"`)
		}
		return []byte(`"error":{"code":-32000,"message":"execution reverted","data":"0xdead"}`)
	})
	p := newPoolWithDialerAndChainID(nativehttp.NewDialer(), 0)
	t.Cleanup(p.Close)
	src, err := p.Join("native", srv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		emissions, err := p.Collect(context.Background(), "eth_call", []any{"0x1"}, CollectOptions{Sources: []*Source{src}})
		if err != nil {
			t.Fatalf("Collect[%d] err = %v, want usable answer", i, err)
		}
		if len(emissions) != 1 {
			t.Fatalf("Collect[%d] emissions = %+v", i, emissions)
		}
		e := emissions[0]
		if !e.Attempted {
			t.Fatalf("Collect[%d] emission not attempted", i)
		}
		if i == 0 && string(e.Val) != `"0x2a"` {
			t.Fatalf("Collect[0] val = %s", e.Val)
		}
		if i == 1 {
			var protocol rpc.ProtocolError
			if !errors.As(e.Err, &protocol) || protocol.ErrorCode() != -32000 {
				t.Fatalf("Collect[1] err = %v, want revert protocol error", e.Err)
			}
			var presence rpc.ErrorDataPresence
			if !errors.As(e.Err, &presence) {
				t.Fatalf("Collect[1] err %v lost presence through the pool", e.Err)
			}
			raw, present := presence.ErrorDataJSON()
			if !present || string(raw) != `"0xdead"` {
				t.Fatalf("Collect[1] presence = %s/%v", raw, present)
			}
		}
	}
	if got := src.Health(); got.Successes != 2 || got.Failures != 0 {
		t.Fatalf("health = %+v, want two healthy observations (reverts stay healthy)", got)
	}
	if src.State() != SourceReady {
		t.Fatalf("state = %v, want SourceReady", src.State())
	}
}

// TestNativeAdapterPoolRateLimitClassifiesThrottling locks the 429 path: the
// emission is a failure, not a usable answer, and the monitor counts explicit
// throttling.
func TestNativeAdapterPoolRateLimitClassifiesThrottling(t *testing.T) {
	srv := newPoolEchoServerHandler(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Error(w, `{"error":"slow down"}`, nethttp.StatusTooManyRequests)
	}))
	p := newPoolWithDialerAndChainID(nativehttp.NewDialer(), 0)
	t.Cleanup(p.Close)
	src, err := p.Join("native", srv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	emissions, err := p.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{src}})
	if !errors.Is(err, ErrAllSourcesDown) {
		t.Fatalf("Collect err = %v, want ErrAllSourcesDown (429 is not a usable answer)", err)
	}
	if len(emissions) != 1 || emissions[0].Err == nil {
		t.Fatalf("emissions = %+v, want one failed emission", emissions)
	}
	var rateLimit rpc.RateLimitError
	if !errors.As(emissions[0].Err, &rateLimit) || !rateLimit.IsRateLimit() {
		t.Fatalf("emission err = %v, want rate-limit capability", emissions[0].Err)
	}
	got := src.Health()
	if got.Failures != 1 || got.RateLimited != 1 || got.Successes != 0 {
		t.Fatalf("health = %+v, want one failure counted as throttling", got)
	}
}

// TestNativeAdapterPoolMalformedIsFailure locks that malformed and empty
// responses stay transport failures through the pool.
func TestNativeAdapterPoolMalformedIsFailure(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "truncated-json", body: `{"jsonrpc":"2.0","id":1,`},
		{name: "missing-result", body: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPoolEchoServer(t, func(n int, req poolCapturedRequest) []byte {
				return []byte(tc.body)
			})
			p := newPoolWithDialerAndChainID(nativehttp.NewDialer(), 0)
			t.Cleanup(p.Close)
			src, err := p.Join("native", srv.srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			emissions, err := p.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{src}})
			if !errors.Is(err, ErrAllSourcesDown) {
				t.Fatalf("Collect err = %v, want ErrAllSourcesDown", err)
			}
			if len(emissions) != 1 || emissions[0].Err == nil {
				t.Fatalf("emissions = %+v, want one failed emission", emissions)
			}
			var protocol rpc.ProtocolError
			if errors.As(emissions[0].Err, &protocol) {
				t.Fatalf("malformed response became a protocol error: %v", emissions[0].Err)
			}
			got := src.Health()
			if got.Failures != 1 || got.Successes != 0 || got.RateLimited != 0 {
				t.Fatalf("health = %+v, want one plain failure", got)
			}
		})
	}
}

// TestDegenerateErrorMemberHealthDifference is the deliberate B-class
// divergence, asserted side by side at pool level: the Geth baseline counts a
// degenerate error member as a healthy usable answer; the native adapter
// rejects it as malformed, so the pool records a failure and no usable
// answer. This is maintainer ruling 2 of the HTTP contract, not a bug.
func TestDegenerateErrorMemberHealthDifference(t *testing.T) {
	srv := newPoolEchoServer(t, func(n int, req poolCapturedRequest) []byte {
		return []byte(`"error":{}`)
	})

	// Geth baseline: healthy usable answer (documented quirk).
	gethPool := newPoolWithDialerAndChainID(standardDialer{}, 0)
	t.Cleanup(gethPool.Close)
	gethSrc, err := gethPool.Join("geth", srv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	gethEmissions, err := gethPool.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{gethSrc}})
	if err != nil {
		t.Fatalf("geth Collect err = %v, want the baseline healthy answer", err)
	}
	if len(gethEmissions) != 1 {
		t.Fatalf("geth emissions = %+v", gethEmissions)
	}
	var gethProtocol rpc.ProtocolError
	if !errors.As(gethEmissions[0].Err, &gethProtocol) || gethProtocol.ErrorCode() != 0 {
		t.Fatalf("geth emission err = %v, want code-0 protocol error", gethEmissions[0].Err)
	}
	if got := gethSrc.Health(); got.Successes != 1 || got.Failures != 0 {
		t.Fatalf("geth health = %+v, want the healthy quirk", got)
	}

	// Native contract: malformed transport failure.
	nativePool := newPoolWithDialerAndChainID(nativehttp.NewDialer(), 0)
	t.Cleanup(nativePool.Close)
	nativeSrc, err := nativePool.Join("native", srv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	nativeEmissions, err := nativePool.Collect(context.Background(), "eth_chainId", nil, CollectOptions{Sources: []*Source{nativeSrc}})
	if !errors.Is(err, ErrAllSourcesDown) {
		t.Fatalf("native Collect err = %v, want ErrAllSourcesDown", err)
	}
	if len(nativeEmissions) != 1 || nativeEmissions[0].Err == nil {
		t.Fatalf("native emissions = %+v", nativeEmissions)
	}
	if !strings.Contains(nativeEmissions[0].Err.Error(), "malformed JSON-RPC response") {
		t.Fatalf("native emission err = %v, want malformed response classification", nativeEmissions[0].Err)
	}
	if got := nativeSrc.Health(); got.Successes != 0 || got.Failures != 1 {
		t.Fatalf("native health = %+v, want one failure (contract health semantics)", got)
	}
}

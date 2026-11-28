// Package http implements EthGuard's native HTTP JSON-RPC transport adapter.
//
// It implements the HTTP contract in docs/design/transport-contract.md on top
// of net/http and internal/rpcwire: strict response validation, project-owned
// error capabilities, bounded reads with context-first precedence, idempotent
// Close that rejects new calls, and no automatic JSON-RPC retry or replay.
//
// internal/runtime selects this adapter for HTTP sources. It shares the wire
// codec with the native WSS adapter and imports no go-ethereum rpc or ethclient.
package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	nethttp "net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/codermaybe/ethguard/internal/limits"
	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// DefaultBodyLimit bounds one response body read using the shared payload limit.
const DefaultBodyLimit = limits.MaxBodyBytes

// maxStatusBody caps how much of a non-2xx body is retained in the error
// chain. The body stays available for diagnostics and text-based
// classification (pool rate-limit rules), but a huge error page cannot
// consume unbounded memory.
const maxStatusBody = 4 << 10

// ErrTransportClosed is returned for calls on a closed transport. It wraps
// the neutral lifecycle sentinel so core packages can classify the failure
// without importing this adapter.
var ErrTransportClosed = fmt.Errorf("http: transport closed: %w", transport.ErrConnectionClosed)

// Dialer dials native HTTP JSON-RPC transports for http and https URLs.
type Dialer struct {
	// BodyLimit bounds one response body read; zero selects DefaultBodyLimit.
	BodyLimit int64
}

// NewDialer returns the native HTTP adapter factory used by internal/runtime.
func NewDialer() Dialer { return Dialer{} }

// DialContext implements transport.Dialer. Dialing performs no network I/O:
// HTTP requests are issued per call and reuse the transport's connection pool.
func (d Dialer) DialContext(ctx context.Context, rawURL string) (transport.Transport, error) {
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("http: parse endpoint: %w", err)
	}
	switch endpoint.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("http: no known transport for URL scheme %q", endpoint.Scheme)
	}
	return newTransport(rawURL, d.bodyLimit()), nil
}

func (d Dialer) bodyLimit() int64 {
	if d.BodyLimit > 0 {
		return d.BodyLimit
	}
	return DefaultBodyLimit
}

// Transport is one native HTTP JSON-RPC connection. It is safe for
// concurrent use.
type Transport struct {
	url    string
	client *nethttp.Client
	conns  *nethttp.Transport

	ids   *rpcwire.IDGen
	limit int64

	closed    atomic.Bool
	closeOnce sync.Once
}

func newTransport(rawURL string, limit int64) *Transport {
	// One base transport per adapter so CloseIdleConnections stays scoped to
	// it. Clone keeps net/http defaults (proxy, keep-alive, idle-constraint
	// behavior), which is the audited connection-reuse baseline.
	// DefaultTransport is a documented replaceable global: when the host
	// application swapped in a custom RoundTripper, fall back to the
	// library's own conservative transport instead of panicking on the
	// assertion.
	conns := &nethttp.Transport{}
	if base, ok := nethttp.DefaultTransport.(*nethttp.Transport); ok {
		conns = base.Clone()
	}
	return &Transport{
		url: rawURL,
		client: &nethttp.Client{
			Transport: conns,
			// One call is exactly one JSON-RPC request: a redirect's second
			// hop would be a body-less GET to an origin that is not the
			// configured source, so 3xx is left to the status classification
			// below instead of being followed. This matches the wss
			// adapter's contract and keeps every adapter fail-closed on
			// redirects.
			CheckRedirect: func(*nethttp.Request, []*nethttp.Request) error {
				return nethttp.ErrUseLastResponse
			},
		},
		conns: conns,
		ids:   rpcwire.NewIDGen(),
		limit: limit,
	}
}

// Call executes one exchange and preserves progress independently of its answer.
func (t *Transport) Call(ctx context.Context, method string, args ...any) rpc.Response {
	exchange := rpc.Exchange{}
	failed := func(err error) rpc.Response {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		exchange.Cause = err
		return rpc.Response{Exchange: exchange}
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	if t.closed.Load() {
		return failed(ErrTransportClosed)
	}
	id := t.ids.Next()
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		return failed(fmt.Errorf("http: encode request: %w", err))
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return failed(fmt.Errorf("http: build request: %w", err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	exchange.Progress = rpc.ExchangeStarted
	resp, err := t.client.Do(req)
	if resp != nil {
		exchange.Progress = rpc.ExchangeResponded
		exchange.HTTPStatus = resp.StatusCode
	}
	if err != nil {
		return failed(err)
	}
	limit := t.limit
	statusFailure := resp.StatusCode < 200 || resp.StatusCode >= 300
	if statusFailure {
		limit = maxStatusBody
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	_ = resp.Body.Close()
	oversized := int64(len(raw)) > limit
	if readErr == nil && !oversized {
		exchange.Progress = rpc.ExchangeCompleted
	}
	if statusFailure {
		status := &statusError{status: resp.Status, code: resp.StatusCode, body: raw, truncated: oversized, cause: readErr}
		if oversized {
			status.body = raw[:limit]
			status.cause = errors.Join(readErr, transport.ErrBodyTooLarge)
		}
		// Status remains the final non-2xx classification unless context terminated.
		// Progress separately records whether the bounded read completed.
		return failed(status)
	}
	if oversized {
		return failed(fmt.Errorf("http: response body exceeds limit %d: %w", limit, transport.ErrBodyTooLarge))
	}
	if readErr != nil {
		return failed(readErr)
	}
	// Interpret complete bytes even when cancellation wins final delivery.
	exchange.Cause = ctx.Err()
	return rpcwire.CompleteCall(raw, id, exchange)
}

// ChainID implements transport.Transport via a plain eth_chainId call and
// the project-owned hex quantity decoder; it does not use ethclient.
func (t *Transport) ChainID(ctx context.Context) (*big.Int, error) {
	response := t.Call(ctx, "eth_chainId")
	if err := response.Err(); err != nil {
		return nil, err
	}
	return decodeHexQuantity(response.Answer.Result)
}

// Close marks the transport closed (rejecting new calls) and releases idle
// keep-alive connections. It is idempotent and does not abort calls that are
// already in flight; their outcome is governed by their own context.
func (t *Transport) Close() {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		t.conns.CloseIdleConnections()
	})
}

// statusError is the native HTTP status failure. It carries the retained
// (bounded) body and structurally satisfies rpc.HTTPStatusError and, for
// 429, rpc.RateLimitError; the assertions live in the test file.
type statusError struct {
	status    string
	code      int
	body      []byte
	truncated bool
	cause     error
}

func (e *statusError) Error() string {
	if len(e.body) == 0 {
		return e.status
	}
	if e.truncated {
		return fmt.Sprintf("%s: %s (truncated)", e.status, e.body)
	}
	return fmt.Sprintf("%s: %s", e.status, e.body)
}

// Unwrap retains an interrupted body read without losing HTTP capabilities.
func (e *statusError) Unwrap() error { return e.cause }

// HTTPStatusCode returns the HTTP status of the failed exchange.
func (e *statusError) HTTPStatusCode() int { return e.code }

// IsRateLimit marks explicit provider throttling; only 429 sets it.
func (e *statusError) IsRateLimit() bool { return e.code == 429 }

// The adapter implements the transport seam and the status wrapper carries
// the project-owned HTTP-status capabilities, all structurally; assertions
// live here so the seam contract cannot drift silently.
var (
	_ transport.Transport = (*Transport)(nil)
	_ transport.Dialer    = Dialer{}
	_ rpc.HTTPStatusError = (*statusError)(nil)
	_ rpc.RateLimitError  = (*statusError)(nil)
)

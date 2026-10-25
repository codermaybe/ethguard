// Package http implements EthGuard's native HTTP JSON-RPC transport adapter.
//
// It speaks the EthGuard native contract (docs/design/http-transport-
// differential.md, "EthGuard 自有 HTTP contract") on top of net/http and
// internal/rpcwire, with no go-ethereum rpc/ethclient dependency: strict
// response validation, project-owned error capabilities, bounded reads with
// context-first precedence, idempotent Close that rejects new calls, and no
// automatic retry or replay.
//
// The package coexists with the Geth compatibility adapter in
// internal/transport; the default production dialer is unchanged until the
// maintainer approves the switch.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	nethttp "net/http"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// DefaultBodyLimit bounds one response body read. It matches the audited
// capture limit of the Geth compatibility adapter (class A).
const DefaultBodyLimit = 32 << 20

// maxStatusBody caps how much of a non-2xx body is retained in the error
// chain. The body stays available for diagnostics and text-based
// classification (pool rate-limit rules), but a huge error page cannot
// consume unbounded memory.
const maxStatusBody = 4 << 10

// ErrTransportClosed is returned for calls on a closed transport. Unlike the
// Geth baseline (whose HTTP Close is a no-op and leaves the adapter usable),
// a native transport blocks new calls once closed (EthGuard contract,
// lifecycle).
var ErrTransportClosed = errors.New("http: transport closed")

// Dialer dials native HTTP JSON-RPC transports for http and https URLs.
type Dialer struct {
	// BodyLimit bounds one response body read; zero selects DefaultBodyLimit.
	BodyLimit int64
}

// NewDialer returns the native HTTP adapter factory. It is intentionally not
// wired into the default production path: the switch is a separate,
// maintainer-approved change.
func NewDialer() Dialer { return Dialer{} }

// DialContext implements transport.Dialer. Dialing performs no network I/O:
// HTTP requests are issued per call, mirroring the audited Geth behavior.
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
	conns := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	return &Transport{
		url:    rawURL,
		client: &nethttp.Client{Transport: conns},
		conns:  conns,
		ids:    rpcwire.NewIDGen(),
		limit:  limit,
	}
}

// CallContext performs one JSON-RPC call. result must be nil, a
// *json.RawMessage (receives the raw result bytes), or another JSON pointer.
func (t *Transport) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if t.closed.Load() {
		return ErrTransportClosed
	}
	id := t.ids.Next()
	body, err := rpcwire.EncodeRequest(id, method, args)
	if err != nil {
		return fmt.Errorf("http: encode request: %w", err)
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// Kept unwrapped so errors.Is resolves context.Canceled and
		// context.DeadlineExceeded exactly as in the audited baseline.
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusErrorFromBody(resp)
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, t.limit+1))
	// Deliberately no drain: the read already decided the outcome, and an
	// oversized or failed body leaves the connection unreusable, which
	// net/http handles when the body is closed.
	_ = resp.Body.Close()

	// Error precedence mirrors the audited class A layering; each layer
	// strictly dominates the next:
	//  1. call-context cancellation or deadline,
	//  2. byte-count overrun (truncated prefixes never reach the decoder),
	//  3. the read failure itself.
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if int64(len(raw)) > t.limit {
		return fmt.Errorf("http: response body exceeds limit %d: %w", t.limit, transport.ErrBodyTooLarge)
	}
	if readErr != nil {
		return readErr
	}

	decoded, err := rpcwire.DecodeResponse(raw, id)
	if err != nil {
		return err
	}
	if decoded.Error != nil {
		return decoded.Error
	}
	return decodeResult(decoded.Result, result)
}

// ChainID implements transport.Transport via a plain eth_chainId call and
// the project-owned hex quantity decoder; it does not use ethclient.
func (t *Transport) ChainID(ctx context.Context) (*big.Int, error) {
	var raw json.RawMessage
	if err := t.CallContext(ctx, &raw, "eth_chainId"); err != nil {
		return nil, err
	}
	return decodeHexQuantity(raw)
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

// decodeResult fills the caller's result target from the raw result bytes.
func decodeResult(raw json.RawMessage, result any) error {
	switch target := result.(type) {
	case nil:
		return nil
	case *json.RawMessage:
		*target = append((*target)[:0], raw...)
		return nil
	default:
		if reflect.ValueOf(result).Kind() != reflect.Pointer {
			return fmt.Errorf("http: result must be pointer or nil, got %T", result)
		}
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("http: decode result: %w", err)
		}
		return nil
	}
}

// statusError is the native HTTP status failure. It carries the retained
// (bounded) body and structurally satisfies rpc.HTTPStatusError and, for
// 429, rpc.RateLimitError; the assertions live in the test file.
type statusError struct {
	status    string
	code      int
	body      []byte
	truncated bool
}

func statusErrorFromBody(resp *nethttp.Response) error {
	// Retain a bounded prefix of the body for diagnostics and text rules;
	// reading one extra byte detects truncation. The remainder is never
	// pulled in and the connection is abandoned on Close.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxStatusBody+1))
	_ = resp.Body.Close()
	e := &statusError{status: resp.Status, code: resp.StatusCode}
	if len(raw) > maxStatusBody {
		e.body = raw[:maxStatusBody]
		e.truncated = true
	} else {
		e.body = raw
	}
	return e
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

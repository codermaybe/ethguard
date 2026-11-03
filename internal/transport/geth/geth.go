// Package geth is the go-ethereum compatibility connection adapter. It is
// the only production package allowed to import ethclient and gethrpc; the
// default build links the native HTTP adapter instead, and this package is
// reached explicitly through gethcompat.
package geth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// Dialer is the go-ethereum compatibility adapter. HTTP uses the
// body-capturing path in this package; WebSocket and other supported schemes
// continue through go-ethereum's DialContext unchanged. BodyLimit bounds one
// captured response body; zero selects the audited default bound.
type Dialer struct {
	BodyLimit int64
}

// StandardDialer is the compatibility dialer with the audited default bound.
type StandardDialer = Dialer

func (d Dialer) DialContext(ctx context.Context, rawURL string) (transport.Transport, error) {
	if client, ok, err := dialHTTPPreservingErrorDataWithLimit(ctx, rawURL, d.BodyLimit); ok {
		if err != nil {
			if client != nil {
				client.Close()
			}
			return nil, err
		}
		return newGethTransport(client, true, d.BodyLimit), nil
	}
	client, err := ethclient.DialContext(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return newGethTransport(client, false, d.BodyLimit), nil
}

// NewGethTransport wraps an existing go-ethereum client without enabling the
// HTTP body-capture path. It is used by in-process test adapters and keeps
// the concrete client inside this package.
func NewGethTransport(client *ethclient.Client) transport.Transport {
	return newGethTransport(client, false, 0)
}

type gethTransport struct {
	client    *ethclient.Client
	capture   bool
	limit     int64
	closed    atomic.Bool
	closeOnce sync.Once
}

func newGethTransport(client *ethclient.Client, capture bool, limit int64) transport.Transport {
	return &gethTransport{client: client, capture: capture, limit: limit}
}

func (t *gethTransport) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if t.closed.Load() {
		// The compatibility baseline left HTTP connections usable after
		// Close; the contract's lifecycle clause wins: closed transports
		// block new calls with the neutral sentinel.
		return transport.ErrConnectionClosed
	}
	if !t.capture {
		return adaptError(t.client.Client().CallContext(ctx, result, method, args...))
	}
	ctx, body := withRawBody(ctx)
	err := t.client.Client().CallContext(ctx, result, method, args...)
	raw, haveBody := body.bytes()
	if err != nil {
		if haveBody {
			err = preserveErrorData(err, raw)
		}
		return adaptError(err)
	}
	// The capturing path owns the raw body, so it can hold the exchange to
	// the contract even where the wrapped client is lenient: trailing
	// garbage and degenerate envelopes fail with the project sentinels
	// instead of a decoded value.
	if haveBody {
		if verr := rpcwire.ValidateBody(raw); verr != nil {
			return verr
		}
	}
	return adaptError(err)
}

func (t *gethTransport) ChainID(ctx context.Context) (*big.Int, error) {
	chainID, err := t.client.ChainID(ctx)
	return chainID, adaptError(err)
}

// Close releases the client and blocks new calls. It is idempotent; calls
// already in flight are governed by their own context.
func (t *gethTransport) Close() {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		t.client.Close()
	})
}

type httpStatusError struct {
	err    error
	status int
}

func (e *httpStatusError) Error() string       { return e.err.Error() }
func (e *httpStatusError) Unwrap() error       { return e.err }
func (e *httpStatusError) HTTPStatusCode() int { return e.status }
func (e *httpStatusError) IsRateLimit() bool   { return e.status == 429 }

// adaptError maps the wrapped client's errors onto the project-owned
// capabilities and the neutral lifecycle sentinel. Geth's decoded jsonError
// satisfies rpc.ProtocolError and rpc.DataError structurally without a
// wrapper; HTTP status needs this one because gethrpc.HTTPError is a struct,
// not a capability. A closed-connection failure additionally carries the
// neutral sentinel so core packages classify the lifecycle without
// importing this adapter.
func adaptError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gethrpc.ErrClientQuit) {
		err = fmt.Errorf("%w (%w)", transport.ErrConnectionClosed, err)
	}
	// The wrapped client reports broken response bodies as raw decode
	// failures and degenerate error members as code-0 protocol errors. Both
	// classes map onto the project sentinels: a broken body is a malformed
	// response, and code zero is not a valid JSON-RPC error code, so a
	// code-0 protocol error can only be a degenerate envelope the contract
	// refuses (a healthy error never counts as a usable answer).
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %v", rpcwire.ErrEmptyResponse, err)
	}
	var protocol gethrpc.Error
	if errors.As(err, &protocol) && protocol.ErrorCode() == 0 {
		return fmt.Errorf("%w: degenerate error member: %v", rpcwire.ErrMalformedResponse, err)
	}
	if isJSONDecodeFailure(err) {
		return fmt.Errorf("%w: %v", rpcwire.ErrMalformedResponse, err)
	}
	var existing interface{ HTTPStatusCode() int }
	if errors.As(err, &existing) {
		return err
	}
	var httpErr gethrpc.HTTPError
	if errors.As(err, &httpErr) {
		return &httpStatusError{err: err, status: httpErr.StatusCode}
	}
	return err
}

// isJSONDecodeFailure reports the decode failures the wrapped client
// surfaces for broken response bodies: malformed JSON, truncated bodies,
// and value-shape mismatches.
func isJSONDecodeFailure(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

// The Adapter's error wrappers satisfy the project-owned protocol-error
// capabilities, so pool, aggregator, write and livecanary never need the
// go-ethereum error interfaces.
var (
	_ rpc.HTTPStatusError = (*httpStatusError)(nil)
	_ rpc.RateLimitError  = (*httpStatusError)(nil)
)

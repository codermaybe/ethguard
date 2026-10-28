// Package geth is the go-ethereum compatibility connection adapter. It is
// the only production package allowed to import ethclient and gethrpc; the
// default build links the native HTTP adapter instead, and this package is
// reached explicitly through gethcompat.
package geth

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

// StandardDialer is the go-ethereum compatibility adapter. HTTP uses the
// body-capturing path in this package; WebSocket and other supported schemes
// continue through go-ethereum's DialContext unchanged.
type StandardDialer struct{}

func (StandardDialer) DialContext(ctx context.Context, rawURL string) (transport.Transport, error) {
	if client, ok, err := dialHTTPPreservingErrorData(ctx, rawURL); ok {
		if err != nil {
			if client != nil {
				client.Close()
			}
			return nil, err
		}
		return newGethTransport(client, true), nil
	}
	client, err := ethclient.DialContext(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return newGethTransport(client, false), nil
}

// NewGethTransport wraps an existing go-ethereum client without enabling the
// HTTP body-capture path. It is used by in-process test adapters and keeps
// the concrete client inside this package.
func NewGethTransport(client *ethclient.Client) transport.Transport {
	return newGethTransport(client, false)
}

type gethTransport struct {
	client  *ethclient.Client
	capture bool
}

func newGethTransport(client *ethclient.Client, capture bool) transport.Transport {
	return &gethTransport{client: client, capture: capture}
}

func (t *gethTransport) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if !t.capture {
		return adaptError(t.client.Client().CallContext(ctx, result, method, args...))
	}
	ctx, body := withRawBody(ctx)
	err := t.client.Client().CallContext(ctx, result, method, args...)
	if err != nil {
		if raw, ok := body.bytes(); ok {
			err = preserveErrorData(err, raw)
		}
	}
	return adaptError(err)
}

func (t *gethTransport) ChainID(ctx context.Context) (*big.Int, error) {
	chainID, err := t.client.ChainID(ctx)
	return chainID, adaptError(err)
}

func (t *gethTransport) Close() { t.client.Close() }

// IsClientQuit reports the compatibility adapter's closed-connection error
// without exposing go-ethereum's sentinel to callers outside this package.
func IsClientQuit(err error) bool { return errors.Is(err, gethrpc.ErrClientQuit) }

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

// The Adapter's error wrappers satisfy the project-owned protocol-error
// capabilities, so pool, aggregator, write and livecanary never need the
// go-ethereum error interfaces.
var (
	_ rpc.HTTPStatusError = (*httpStatusError)(nil)
	_ rpc.RateLimitError  = (*httpStatusError)(nil)
)

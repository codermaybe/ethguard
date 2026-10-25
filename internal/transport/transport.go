// Package transport contains the connection adapters used by pool.
//
// The package owns protocol-specific implementations; pool only consumes the
// small Transport and Dialer seams defined here.
package transport

import (
	"context"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// Transport is one long-lived upstream connection. It deliberately exposes
// only a raw JSON-RPC call, the chain identity probe used by source admission,
// and lifecycle close. Protocol adapters may attach structured wire errors to
// the returned error without expanding this interface.
type Transport interface {
	CallContext(context.Context, any, string, ...any) error
	ChainID(context.Context) (*big.Int, error)
	Close()
}

// Dialer creates one long-lived Transport for an endpoint.
type Dialer interface {
	DialContext(context.Context, string) (Transport, error)
}

// StandardDialer is the current go-ethereum compatibility adapter. HTTP uses
// the body-capturing path in this package; WebSocket and other supported
// schemes continue through go-ethereum's DialContext unchanged.
type StandardDialer struct{}

func (StandardDialer) DialContext(ctx context.Context, rawURL string) (Transport, error) {
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
// HTTP body-capture path. It is used by in-process test adapters and keeps the
// concrete client inside this package.
func NewGethTransport(client *ethclient.Client) Transport {
	return newGethTransport(client, false)
}

type gethTransport struct {
	client  *ethclient.Client
	capture bool
}

func newGethTransport(client *ethclient.Client, capture bool) Transport {
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
// without exposing go-ethereum's sentinel to pool.
func IsClientQuit(err error) bool { return errors.Is(err, gethrpc.ErrClientQuit) }

type httpStatusError struct {
	err    error
	status int
}

func (e *httpStatusError) Error() string       { return e.err.Error() }
func (e *httpStatusError) Unwrap() error       { return e.err }
func (e *httpStatusError) HTTPStatusCode() int { return e.status }
func (e *httpStatusError) IsRateLimit() bool   { return e.status == 429 }

func adaptError(err error) error {
	if err == nil {
		return nil
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

var _ interface {
	error
	HTTPStatusCode() int
	IsRateLimit() bool
	Unwrap() error
} = (*httpStatusError)(nil)

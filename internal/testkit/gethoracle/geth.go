// Package gethoracle provides a go-ethereum transport for differential tests and probes.
package gethoracle

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

func (t *gethTransport) Call(ctx context.Context, method string, args ...any) rpc.Response {
	if err := ctx.Err(); err != nil {
		return rpc.Response{Exchange: rpc.Exchange{Cause: err}}
	}
	if t.closed.Load() {
		return rpc.Response{Exchange: rpc.Exchange{Cause: transport.ErrConnectionClosed}}
	}
	exchange := rpc.Exchange{Progress: rpc.ExchangeStarted}
	var box *rawBodyBox
	if t.capture {
		ctx, box = withRawBody(ctx)
	}
	var result json.RawMessage
	err := t.client.Client().CallContext(ctx, &result, method, args...)
	raw, haveBody, progress, status := box.snapshot()
	exchange.Progress = progress
	exchange.HTTPStatus = status
	if err != nil && haveBody {
		err = preserveErrorData(err, raw)
	}
	err = adaptError(err)
	if exchange.HTTPStatus != 0 && (exchange.HTTPStatus < 200 || exchange.HTTPStatus >= 300) {
		exchange.Cause = err
		if ctx.Err() != nil {
			exchange.Cause = ctx.Err()
		}
		return rpc.Response{Exchange: exchange}
	}
	response := rpc.Response{Exchange: exchange}
	// Only captured complete bytes establish missing/invalid protocol answers.
	// Uncaptured failures do not reveal response progress through Geth's API.
	if haveBody && err == nil {
		if validation := rpcwire.ValidateBody(raw); validation != nil {
			kind := rpc.AnswerInvalid
			if errors.Is(validation, rpcwire.ErrEmptyResponse) {
				kind = rpc.AnswerMissing
			}
			response.Answer = rpc.Answer{Kind: kind, Cause: validation}
			response.Exchange.Cause = ctx.Err()
			return response
		}
	}
	var protocol rpc.ProtocolError
	switch {
	case errors.As(err, &protocol):
		response.Exchange.Progress = rpc.ExchangeCompleted
		response.Answer = rpc.ProtocolAnswer(err)
	case err == nil:
		response.Exchange.Progress = rpc.ExchangeCompleted
		response.Answer = rpc.Answer{Kind: rpc.AnswerResult, Result: result}
	case haveBody && ctx.Err() == nil:
		kind := rpc.AnswerInvalid
		if errors.Is(err, rpcwire.ErrEmptyResponse) {
			kind = rpc.AnswerMissing
		}
		response.Answer = rpc.Answer{Kind: kind, Cause: err}
	default:
		response.Exchange.Cause = err
	}
	if ctx.Err() != nil {
		response.Exchange.Cause = ctx.Err()
	}
	return response
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

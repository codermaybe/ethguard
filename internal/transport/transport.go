// Package transport is the Port-only physical home of the connection seam:
// the Transport and Dialer interfaces pool consumes, and the neutral
// sentinels core packages classify on. It owns no adapter implementation —
// the native HTTP adapter lives in internal/transport/http and the go-ethereum
// compatibility adapter in internal/transport/geth — so the default build
// never links ethclient.
package transport

import (
	"context"
	"errors"
	"math/big"
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

// ErrConnectionClosed is the neutral sentinel for a call attempted on a
// transport after Close. Every adapter maps its own closed-connection error
// onto this sentinel so core packages classify the lifecycle without knowing
// the adapter. Close is idempotent, and calls already in flight are governed
// by their own context, not by Close.
var ErrConnectionClosed = errors.New("transport: connection closed")

// ErrBodyTooLarge classifies a response body that exceeded the capture or
// read byte-count limit. An oversized body is never handed to a decoder as if
// it were a complete JSON-RPC response: the transport fails the call with
// this error instead, so no adapter vouches for truncated bytes.
var ErrBodyTooLarge = errors.New("transport: response body exceeds limit")

// Package ipc connects the shared duplex RPC owner to local sockets and pipes.
package ipc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/codermaybe/ethguard/internal/endpoint"
	"github.com/codermaybe/ethguard/internal/limits"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/streamrpc"
)

const DefaultReadLimit = limits.MaxBodyBytes

var ErrUnsupportedPlatform = errors.New("ipc: endpoint is unsupported on this platform")

// Dialer consumes frozen IPC dial targets. Configuration owns URI parsing and
// relative-path resolution; physical dialing never reinterprets the cwd.
type Dialer struct {
	ReadLimit       int64
	PendingMessages int
	PendingBytes    uint64
}

func NewDialer() Dialer                           { return Dialer{} }
func (Dialer) SupportsBoundedSubscriptions() bool { return true }

func (d Dialer) DialContext(ctx context.Context, target string) (transport.Transport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(target) && !strings.HasPrefix(target, `\\.\pipe\`) {
		return nil, fmt.Errorf("ipc: expected a frozen absolute path or named pipe target")
	}
	parsed, err := endpoint.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("ipc: invalid dial target: %w", err)
	}
	if parsed.Transport() != "ipc" || parsed.DialTarget() != target {
		return nil, fmt.Errorf("ipc: dial target must already be normalized")
	}
	raw, err := dialLocal(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	limit := d.ReadLimit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	return &Transport{Transport: streamrpc.New(newConn(raw, limit), streamrpc.Options{PendingMessages: d.PendingMessages, PendingBytes: d.PendingBytes})}, nil
}

// Transport delegates all call, subscription and lifecycle state to streamrpc.
type Transport struct{ *streamrpc.Transport }

var _ transport.BoundedSubscriberDialer = Dialer{}
var _ transport.Transport = (*Transport)(nil)

//go:build windows

package ipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
	"github.com/codermaybe/ethguard/internal/endpoint"
)

func dialLocal(ctx context.Context, target endpoint.Endpoint) (net.Conn, error) {
	if target.Kind() != endpoint.NamedPipe {
		return nil, ErrUnsupportedPlatform
	}
	return winio.DialPipeContext(ctx, target.DialTarget())
}

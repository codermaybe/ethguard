//go:build unix

package ipc

import (
	"context"
	"net"

	"github.com/codermaybe/ethguard/internal/endpoint"
)

func dialLocal(ctx context.Context, target endpoint.Endpoint) (net.Conn, error) {
	if target.Kind() != endpoint.UnixSocket {
		return nil, ErrUnsupportedPlatform
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", target.DialTarget())
}

//go:build !unix && !windows

package ipc

import (
	"context"
	"net"

	"github.com/codermaybe/ethguard/internal/endpoint"
)

func dialLocal(context.Context, endpoint.Endpoint) (net.Conn, error) {
	return nil, ErrUnsupportedPlatform
}

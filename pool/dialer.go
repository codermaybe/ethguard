package pool

import (
	"context"

	"github.com/ethereum/go-ethereum/ethclient"
)

// dialer establishes long-lived RPC connections to upstream endpoints.
//
// It is an interface so that tests can inject a fake dialer instead of reaching
// a real network. The production dialer dials over the wire and returns a
// *ethclient.Client owned by a single Source. A source reuses its client for
// every request; it never dials per call (see architecture.md).
type dialer interface {
	DialContext(ctx context.Context, rawURL string) (*ethclient.Client, error)
}

type standardDialer struct{}

// DialContext opens an RPC connection to the given raw URL.
func (standardDialer) DialContext(ctx context.Context, rawURL string) (*ethclient.Client, error) {
	return ethclient.DialContext(ctx, rawURL)
}

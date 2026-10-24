package pool

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Transport is the pool-owned minimum upstream seam. It exposes one raw
// JSON-RPC call, the identity read needed for chain validation, and lifecycle
// close. It deliberately carries no method policy, block model, subscription,
// filter, or quorum behavior.
type Transport interface {
	CallContext(context.Context, any, string, ...any) error
	ChainID(context.Context) (*big.Int, error)
	Close()
}

// gethTransport adapts the current go-ethereum client to the pool seam. The
// concrete client remains an adapter detail and never leaves this package.
type gethTransport struct{ client *ethclient.Client }

func newTransport(client *ethclient.Client) Transport {
	return &gethTransport{client: client}
}

func (t *gethTransport) CallContext(ctx context.Context, result any, method string, args ...any) error {
	return t.client.Client().CallContext(ctx, result, method, args...)
}

func (t *gethTransport) ChainID(ctx context.Context) (*big.Int, error) {
	return t.client.ChainID(ctx)
}

func (t *gethTransport) Close() { t.client.Close() }

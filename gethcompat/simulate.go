package gethcompat

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// SimulateClient is the explicit go-ethereum compatibility adapter for
// eth_simulateV1. It owns the concrete go-ethereum client created by
// DialSimulate and exposes the official parameter and result types.
//
// This adapter is deliberately separate from ethguard.Client and
// ethguard.EthClient. Importing gethcompat opts into go-ethereum's ethclient
// package; the default ethguard build remains native and does not expose
// SimulateV1 on its EthClient view. Calls made through this adapter therefore
// have go-ethereum's single-source semantics and do not produce EthGuard
// CallResult evidence.
type SimulateClient struct {
	client *ethclient.Client
}

// DialSimulate dials one endpoint for the explicit eth_simulateV1 adapter.
// The returned adapter owns the connection and must be closed by the caller.
func DialSimulate(ctx context.Context, rawURL string) (*SimulateClient, error) {
	client, err := ethclient.DialContext(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return &SimulateClient{client: client}, nil
}

// WrapSimulate exposes eth_simulateV1 on an existing go-ethereum client. The
// returned adapter shares the client's lifecycle; Close closes that client.
func WrapSimulate(client *ethclient.Client) (*SimulateClient, error) {
	if client == nil {
		return nil, fmt.Errorf("gethcompat: nil ethclient")
	}
	return &SimulateClient{client: client}, nil
}

// SimulateV1 executes transactions on top of a base state using the exact
// go-ethereum v1.17.10 signature and official wire types.
func (c *SimulateClient) SimulateV1(ctx context.Context, opts ethclient.SimulateOptions, blockNrOrHash *gethrpc.BlockNumberOrHash) ([]ethclient.SimulateBlockResult, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("gethcompat: nil SimulateClient")
	}
	return c.client.SimulateV1(ctx, opts, blockNrOrHash)
}

// Close releases the underlying go-ethereum connection. It is safe to call
// on a nil adapter.
func (c *SimulateClient) Close() {
	if c != nil && c.client != nil {
		c.client.Close()
	}
}

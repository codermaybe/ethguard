package ethguard

import (
	"fmt"

	"ethguard/aggregator"
	"ethguard/config"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// Client is the single entry point for application code.
// Multiple goroutines may use one Client concurrently; connections are owned
// and reused by the internal RPC management pool.
type Client struct {
	pool       *pool.Pool
	aggregator *aggregator.Aggregator
	policies   rpc.Registry
}

// New builds a Client from an already-loaded configuration.
// Upstreams are registered into the RPC pool with endpoint deduplication.
// Dialing each source is deferred until its first call.
func New(cfg config.Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	rpcPool := pool.NewPoolWithChainID(cfg.ChainID)
	for _, upstream := range cfg.Upstreams {
		if _, err := rpcPool.Join(upstream.ID, upstream.Endpoint); err != nil {
			return nil, fmt.Errorf("ethguard: register %q: %w", upstream.Endpoint, err)
		}
	}

	policies := rpc.Registry{
		"eth_chainId":        methods.ChainIDPolicy{},
		"eth_getBlockByHash": methods.BlockByHashPolicy{},
	}
	return &Client{
		pool:       rpcPool,
		aggregator: aggregator.New(rpcPool, policies),
		policies:   policies,
	}, nil
}

// Pool exposes the underlying multi-source manager for advanced use.
// Prefer Client methods for normal application code.
func (c *Client) Pool() *pool.Pool {
	return c.pool
}

// Close releases all upstream connections owned by the client.
// It is safe to call more than once.
func (c *Client) Close() {
	c.pool.Close()
}

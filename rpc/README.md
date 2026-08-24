# RPC

This package contains the normalized JSON-RPC transport and decision model.

It will own request, response, transport, and normalized error types shared by
the pool and aggregator. It will not select upstreams or decide whether
responses agree.

The transport supports arbitrary JSON-RPC methods so unsupported methods can
use explicit default forwarding.

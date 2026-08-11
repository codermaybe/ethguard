# RPC

This directory is reserved for the normalized JSON-RPC transport model.

It will own request, response, transport, and normalized error types shared by
the pool and aggregator. It will not select upstreams or decide whether
responses agree.

The transport must support arbitrary JSON-RPC methods so that unsupported
methods can use explicit default forwarding.

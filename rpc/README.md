# RPC

This package holds the normalized JSON-RPC transport and decision model:
the `Request`/`Observation`/`Decision` types, the `Policy` contract and
`Registry`, and the shared resolution strategies — strict consensus with
quorum options, lower-median selection for variable results, upstream-local
selection, transparent forwarding, and the anchor decorators
(`WithAnchor`, `WithAnchorHeight`, `WithAnchorHeightFields`,
`RejectPending`).

The line against `rpc/methods`: everything here is true of any method and
any JSON; Ethereum wire shapes and per-method normalization live in
`rpc/methods`, whose policies compose the strategies this package provides.
Unknown methods fall through the registry to explicit forwarding, which
never claims `Verified`.

This package does not select upstreams or own connections; `SourceRef`
keeps it decoupled from pool's connection and health implementation.

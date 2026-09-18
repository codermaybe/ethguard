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

## Generic observation reduction

`AggregateFields` is the first seam of the two-chamber model. It performs
strict, method-independent JSON observation reduction: nested values are
indexed by JSON Pointer path, equal values are grouped by source, and only a
single value that reaches the caller's quorum enters `CommonObservation.Common`.
Values below quorum are retained as `Residuals`; paths with multiple
quorum-qualified values are retained as `Conflicts`. The result records source
identity and raw values, but does not decide whether a field is an Ethereum
standard field or how a scalar should be interpreted.

Method packages can project `Common` into their locked, method-specific core
fields and carry the remaining observations as extensions. OpenRPC is not a
runtime dependency of this reduction path.

This package does not select upstreams or own connections; `SourceRef`
keeps it decoupled from pool's connection and health implementation.

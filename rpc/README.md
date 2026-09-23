# RPC

This package holds the normalized JSON-RPC transport and decision model:
the `Request`/`Observation`/`Decision` types, the `Policy` contract and
`Registry`, and the shared resolution strategies — strict corroboration with
quorum options, lower-median selection for variable results, upstream-local
selection, transparent forwarding, and the anchor decorators
(`WithAnchor`, `WithAnchorHeight`, `WithAnchorHeightFields`,
`RejectPending`).

The line against `rpc/methods`: everything here is true of any method and
any JSON; Ethereum wire shapes and per-method normalization live in
`rpc/methods`, whose policies compose the strategies this package provides.
Unknown methods fall through the registry to explicit forwarding, which
never claims `Corroborated`.

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

An aggregate `Decision` may expose the same source-backed result through
`Decision.Observed` when the client is running in corroborated mode. Forwarded
calls intentionally do not make a common-set claim.

Callers handling an unregistered method can use `Decision.ObservedView()` as
an explicit observation-only contract. Its dynamic JSON value and CommonSet
are not a typed or `Corroborated` Ethereum result.

This package does not select upstreams or own connections; `SourceRef`
keeps it decoupled from pool's connection and health implementation.


## Method layers

Ethereum method behavior is not decided in this package. Wire normalization,
object requiredness, request admission, and typed projection stay separate;
see `rpc/methods/README.md`. A comparison key is not an admission decision,
and a typed decode failure is not a corroboration result.

# RPC

This package holds the normalized JSON-RPC transport and decision model:
the `Request`/`Observation`/`Decision` types, the `Policy` contract and
`Registry`, and the shared resolution strategies — dynamic fixed-basis
corroboration, lower-median selection for variable results, upstream-local
selection, transparent forwarding, and the anchor decorators
(`WithAnchor`, `WithAnchorHeight`, `WithAnchorHeightFields`,
`RejectPending`).

The line against `rpc/methods`: everything here is true of any method and
any JSON; Ethereum wire shapes and per-method normalization live in
`rpc/methods`, whose policies compose the strategies this package provides.
Unknown methods fall through the registry to explicit forwarding, which
never claims `Corroborated`.

## Generic observation reduction

`AggregateFields` is the evaluation seam. It performs strict,
method-independent JSON observation reduction: nested values are indexed by
JSON Pointer path, equal values are grouped by source, and only one value that
reaches the invocation threshold enters `Evaluation.Accepted`. Values below
the threshold and paths with multiple qualified values remain in
`Evaluation.Diagnostics`. The evaluator records source identity and raw
values, but does not decide whether a field is an Ethereum standard field or
how a scalar should be interpreted.

Method packages project `Accepted` into their locked, method-specific Core
fields. Accepted non-core fields become optional extensions; diagnostics never
become extensions. OpenRPC is not a runtime dependency of this reduction path.

An aggregate `Decision` exposes only the selected Core value, accepted
extensions, and optional diagnostics. It does not expose a second observation
view or require callers to understand per-source field categories.

This package does not select upstreams or own connections; `SourceRef`
keeps it decoupled from pool's connection and health implementation.


## Method layers

Ethereum method behavior is not decided in this package. Wire normalization,
object requiredness, request admission, and typed projection stay separate;
see `rpc/methods/README.md`. A comparison key is not an admission decision,
and a typed decode failure is not a corroboration result.

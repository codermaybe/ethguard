# Aggregator

The orchestration seam between the upstream pool and per-method policies. It
accepts a normalized RPC request, looks up its method policy, delegates one
bounded fan-out to the pool, resolves the emissions through the policy, and
returns a classified result with evidence.

It owns:

- the prepare → collect → resolve pipeline (`Aggregator.Aggregate`);
- faults attached to decisions (`Decision.Faults`), keeping partial
  fan-outs diagnosable;
- exactly one observability event per aggregate decision (decision 0008).

It does not own Ethereum method semantics (those live in `rpc/methods`
policies), connection lifecycle or concurrency bounds (`pool.Collect`),
telemetry storage, gas optimization, nonce management, signing, or
transaction retries.

Resolution semantics — strict corroboration, quorum options, anchoring,
median and local selection, and transparent forwarding that never claims
`Corroborated` — are implemented by the strategies in the parent `rpc` package
and selected per method; see `docs/decisions.md` 0006–0013.

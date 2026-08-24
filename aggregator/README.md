# Aggregator

This directory is reserved for the deep aggregation module.

Its intended interface accepts a normalized RPC request and a method policy,
collects responses from eligible upstreams, and returns a classified result
with evidence.

It owns:

- bounded concurrent collection and cancellation;
- response grouping and quorum evaluation;
- `Verified`, `Selected`, `Forwarded`, and `Conflict` outcomes;
- evidence describing supporting and conflicting upstreams;
- high-confidence feedback to the upstream pool.

It does not own Ethereum method semantics, connection lifecycle, telemetry
storage, gas optimization, nonce management, signing, or transaction retries.

The initial implementation exposes `Aggregator.Aggregate` and starts with
strict chain-ID agreement plus transparent forwarding for unknown methods.

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

No Go interface is declared yet. The interface should be introduced together
with the first real implementation and its tests.

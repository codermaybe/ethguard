# RPC data contracts

This module owns public Request, Response, Observation, Decision, source summaries, write results and evidence. It also owns protocol error views, immutable-value copies, request-shape validation and pure JSON/quantity helpers. It does not assemble plans, resolve observations, run adjudication or depend on internal execution packages.

Execution mechanisms belong to internal/rpcengine; method semantics and plans belong to internal/methods. Callers and object modules share the public data contracts directly without importing either execution owner.

See [execution topology](../docs/design/codebase-topology.md), [method contracts](../docs/design/method-contracts.md) and [result delivery](../docs/design/return-value-design.md) for the complete current contracts.

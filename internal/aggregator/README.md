# Aggregator

This module owns one invocation's orchestration between collection and the compiled rpcengine.ReadPlan. It freezes membership, prepares and snapshots requests, establishes any required reference, collects responses, runs raw admission or the frozen canonical processor route, resolves outcomes, and assembles faults, projection, material, health and one decision event.

Prepared wire parameters and Decision.Prepared agree byte for byte. Admission reads a frozen semantic request with json.Number values, so caller marshalers and numeric conversion cannot make validation observe a different request. Canonical observations retain original source records while carrying immutable processed values separately.

The aggregator does not interpret Ethereum schemas or fields, own connection lifecycle, choose a second quorum algorithm or re-run raw hooks on canonical failures. Internal processor contract errors are distinct from provider faults; all exit paths retain final health accounting and cleanup responsibilities.

The complete current flow and route contract live in [module topology](../../docs/design/codebase-topology.md). [Method contracts](../../docs/design/method-contracts.md) own semantics and thresholds; [verification](../../docs/design/method-verification-pipeline.md) owns route comparisons and evidence. Implementation optimization waits for the documentation checkpoint and must not transfer orchestration complexity into method owners.

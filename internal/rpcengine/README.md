# RPC execution engine

This module owns Resolver, ReadPlan, Invocation, Basis, Outcome and generic adjudication. It implements Strict, Median, Local, CallRevert and forwarding behavior, the canonical processor/result/observation seam, field aggregation and write propagation adjudication. Its algorithms consume public rpc data contracts; it owns no connections or Ethereum field schemas.

internal/methods assembles registered method plans. The internal Registry can explicitly fall through to Forwarded, while public Client.Call checks method authorization. internal/aggregator coordinates collection and execution; internal/reference can consume the shared engine while retaining its own HeadReader and HeadCodec ports.

AggregateFields reduces generic JSON observations by JSON Pointer and source identity. A unique value meeting the invocation threshold may enter Accepted; insufficient or conflicting values remain Diagnostics. Method projectors decide standard-field membership, protected output and extensions. Diagnostics do not become protected fields or a second business return.

KeySource is explicit: canonical bytes or a declared normalizer key. Encoder presence does not imply clustering, and Median/Local remain Selected. The canonical route consumes validated immutable results without re-running raw normalization or encoding; failures preserve their defined internal/provider distinction. Normalize remains available to the public Client helper and tools that need standalone normalization.

See [execution topology](../../docs/design/codebase-topology.md), [method contracts](../../docs/design/method-contracts.md) and [result delivery](../../docs/design/return-value-design.md) for the complete current contracts.

# RPC

This module owns Request, Observation, Decision, ReadPlan, Policy, resolver outcomes and generic evidence. It implements Strict, block-supporting primitives, Median, Local and CallRevert behavior, plus the canonical processor/result/observation seam. It does not own connections or Ethereum method field schemas.

rpc/methods assembles the registered method plans. The low-level Registry can explicitly fall through to Forwarded, but the public Client.Call rejects unknown methods before that escape; low-level capability is not public method authorization.

AggregateFields reduces generic JSON observations by JSON Pointer and source identity. A unique value meeting the invocation threshold may enter Accepted; insufficient or conflicting values remain Diagnostics. Method projectors decide standard-field membership, protected output and extensions. Diagnostics do not become protected fields or a second business return.

KeySource is explicit: canonical bytes or a declared normalizer key. Encoder presence does not imply clustering, and Median/Local remain Selected. The canonical route consumes validated immutable results without re-running raw normalization or encoding; failures preserve their defined internal/provider distinction.

See [execution topology](../docs/design/codebase-topology.md), [method contracts](../docs/design/method-contracts.md) and [result delivery](../docs/design/return-value-design.md) for the complete current contracts.

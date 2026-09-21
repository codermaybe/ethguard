# RPC Method Policies

This directory holds the Ethereum wire semantics: thin per-method assembly
(`eth_*.go`: constant + `XxxPolicy()` + wiring) over per-object vertical
slices (`receipt.go`, `transaction.go`, `block.go`, `logs.go`); their tests
collect alongside. Shapes used by one method only stay in `eth_*.go` (R1);
slices exist only for objects shared by ≥2 methods or carrying cryptographic
commitments. The comparison whitelists compile at init from the trimmed
`internal/spec/methods.json` (no generated code, no freshness gap); slices own
requiredness. The parent `rpc` package owns the `Policy` contract, the
`Registry`, and shared resolution strategies. This directory owns concrete
Ethereum method entrypoints and method-specific binding. Shared wire and object
implementations live in `rpc/internal`; standard projection and Carrier
assembly live in `rpc/projection`.

Wire and object implementations are deliberately outside this directory:
`rpc/internal/wire` owns JSON normalization and spec-driven pruning, while
`rpc/internal/objects` owns shared block, receipt, transaction, log, and fee-history
semantics. Method files retain only the request binding and policy assembly.

A policy describes how its method is prepared, normalized, compared,
anchored, and classified. Initial policy groups are:

- deterministic reads;
- block-anchored execution and state reads;
- variable results;
- provider-local observations.

Unknown or unsupported methods never get a file here; they fall through the
registry to transparent forwarding. Default forwarding must produce
`Forwarded`, never `Corroborated`.

## Standard projection

The public `rpc/projection` package owns the compile-time standard field declarations and projects the source-backed `CommonSet` produced by `rpc`. Declared paths become core fields,
undeclared common paths become extensions, and missing required paths remain
an explicit method error. OpenRPC is used only as an offline CI reference and
is not loaded by the production projection path.

Projectors are assembled by the client with a method-keyed projector map. A
method policy therefore keeps its existing admission, anchoring, and
corroboration capabilities; adding a projection does not require wrapping or
reimplementing that policy.

The current client assembly reuses the same projector for receipt, block,
transaction, balance, and call methods. Only their compile-time field lists
vary; array-shaped methods such as logs still require a dedicated projection
shape before they are registered.

## Coverage inventory

The package exposes `Catalog()` and `RegisteredNames()` as a read-only method
coverage index. The inventory distinguishes three cases:

| Kind | Meaning | Current examples |
| --- | --- | --- |
| Registered | A method-aware policy is installed in the default client registry. | `eth_gasPrice`, `eth_maxPriorityFeePerGas`, `net_peerCount`, `eth_syncing`, `net_listening` |
| Derived | The root client provides the value without fan-out. | No current catalog entry; reserved for values derived from an established reference |
| Unsupported | A known method is intentionally absent until its contract is designed. | `eth_getProof` |
| Write | Supported through the write pipeline outside the read registry. | `eth_sendRawTransaction` |

Registered methods without an adapter-shaped wrapper remain available through
the root client or `Client.Call`; the catalog records that distinction in the
`Adapter` field. Any method outside the catalog uses `rpc.ForwardPolicy`: the
pool attempts sources in order, stops on the first transport-successful value
or JSON-RPC answer, and returns `Forwarded`. It never retries a non-idempotent
method by broadcasting it to the fleet and never upgrades an unknown answer to
`Corroborated`.

Gas estimates, fee observations, and transaction-count reads may have
policies here. Gas optimization, nonce allocation, transaction signing,
broadcast retries, and replacement transactions do not.

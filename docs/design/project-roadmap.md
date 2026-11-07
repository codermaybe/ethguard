# EthGuard current roadmap

Status: **v1.17.5 business-method completion is in progress; WSS
multi-source aggregation is implemented with evidence gaps remaining**
(2026-11-07).

The external contract is defined only by the
[go-ethereum compatibility policy](go-ethereum-compatibility-policy.md). A
release names one locked go-ethereum version and declares either `complete` or
does not claim compatibility. Internal method families, test batches, and
agent assignments are implementation details and do not create public subset
versions.

The retained contract documents (`t09-contract.md`, `live-canary-contract.md`,
and `return-value-design.md`) preserve executable invariants and evidence rules.
This roadmap and the current method matrix are authoritative for present status
and sequencing.

EthGuard's `Corroborated` remains provider-backed cross-source evidence, not
Ethereum consensus, canonical proof, or cryptographic finality. 架构收敛仍受
execution、quorum、reference 和 typed 边界约束。

## Current facts

- `go.mod` locks go-ethereum v1.17.5.
- The locked `ethclient.Client` type has 48 exported receiver methods. `Client()`
  is a concrete rpc accessor and is explicitly outside the EthGuard business
  facade; the compatibility target is the remaining 47 methods. The current
  46-method check is an internal inventory checkpoint, not a release claim.
- The root `Client` remains the evidence facade. `Client.EthClient()` must
  eventually expose the complete locked-version method set while sharing the
  same runtime, typed projection, transport, and lifecycle implementation.
- `gethcompat.SimulateClient` is an explicit single-source compatibility
  adapter for `eth_simulateV1`. It imports the official go-ethereum
  `ethclient` types only through the opt-in `gethcompat` package and does not
  extend the default `Client.EthClient()` surface or provide EthGuard evidence.
- Native HTTP and raw WSS use the same transport seam. WSS subscriptions are a
  separate capability and cannot be inferred from unary request/response
  support.
- `internal/runtime` is the sole production composition root;
  `internal/typed` is the sole production typed projection;
  `internal/verification` owns cross-module facade verification.
- The acceptance baseline includes `go test ./... -race -count=1` in addition
  to the ordinary full test run.

## Execution plan

1. **Method and API contract.** Keep the 47-method target, signatures,
   pending source-local semantics, derived transaction behavior, synchronous
   write behavior, `SimulateV1`, and the three official subscriptions in one
   reviewed contract. Keep `Client()` excluded and test that exclusion.
2. **Unary completion.** Add root execution, typed projection, method plans,
   error/lifecycle behavior, and focused fixtures for the one remaining
   business method. `TransactionCount`, `PendingTransactionCount`,
   `TransactionInBlock`, and `TransactionSender` now have
   independent plans and facade projections. No unknown `Call` or `ErrUnsupported` placeholder satisfies this
   step. The optional `gethcompat.SimulateClient` preserves the official
   `SimulateV1` signature for callers that explicitly accept single-source
   go-ethereum semantics; it does not close the default 47-method
   `EthClient` compatibility gate. Closing that gate still requires a
   reviewed root/view integration contract for `SimulateV1`.
3. **WSS subscription capability.** The WSS adapter now has an explicit
   `eth_subscribe`/`eth_unsubscribe` capability beside (not inside) unary
   `Transport`, with ID-filtered bounded notification streams and race-tested
   cancellation. The capability is now bound to `EthClient`'s
   `ethereum.Subscription` methods for one explicit WSS source; owner `Close`,
   async errors, and source ownership are covered by the current subscription
   tests. `SimulateV1` remains a separate compatibility decision because its
   official parameter/result types require importing `go-ethereum/ethclient`,
   which the native default build currently forbids.
4. **Multi-source WSS semantics.** Connect source snapshots, quorum,
   duplicate/reorg/removed handling, bounded backpressure, and per-source
   reconnect generations for new heads, logs, and transaction receipts. The
   root facade now runs fixed-snapshot strict quorum aggregation with stale
   session filtering for these three WSS streams. The `newHeads` path also
   performs a same-source unary Pull after the soft window, with physical
   session checks before and after the request and a per-context hard
   quarantine. The `newHeads` path verifies skipped-height gaps through the
   same Pull window; successful verification continues without replay, while
   an unresolved gap terminates through structured `SubscriptionGapError` on
   `Err()`. Logs and transaction receipts have same-source Pull compensation;
   height-gap diagnostics remain specific to `newHeads`.
5. **Transport evidence.** Run matching HTTP/WSS fixtures for every
   completed unary method and the subscription lifecycle matrix. Update the
   live method matrix after each method reaches all required gates.
6. **Release and audit.** Run the full normal/race/vet/release suite and the
   standards/spec review. Publish `EthClient: complete` only when all 47
   methods and their transport/lifecycle evidence are closed.

## Topology invariants

1. Keep the public root package as the facade and preserve the existing deep
   modules (`runtime`, `typed`, `rpc`, `rpc/methods`, `aggregator`, `pool`, and
   transport adapters). Do not create shallow forwarding packages.
2. Keep black-box facade verification under `internal/verification`; leave
   only tests that require root unexported state beside the root package.
3. Treat `method-transport-coverage.md` as the single live method matrix.
   Every missing locked-version method remains an explicit gap until it has a
   root mapping, official view signature, typed projection, error/lifecycle
   behavior, and the required transport evidence.
4. Implement or reject the remaining official methods as complete semantic
   contracts. Unknown forwarding and `ErrUnsupported` do not satisfy the
   compatibility target.
5. On a go-ethereum upgrade, generate the old/new method diff first, then
   close every changed method and update the lock, matrix, tests, and release
   declaration together.

## Acceptance

The roadmap is current when:

- canonical documents link only to current documents;
- root black-box tests are separated from production ownership without
  changing test content or behavior;
- `go list ./...`, `go test ./... -count=1`, `go vet ./...`, the relevant race
  suites, and `scripts/release_check.sh` pass;
- the method matrix names every locked-version method and its current gap;
- no external release text describes an intermediate method subset as a
  compatibility version.

The detailed release gates remain in [release-contract.md](release-contract.md)
and the executable method rules remain in
[method-verification-pipeline.md](method-verification-pipeline.md).

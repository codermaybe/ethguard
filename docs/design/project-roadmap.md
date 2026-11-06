# EthGuard current roadmap

Status: **topology and document consolidation in progress; external target is
the complete `ethclient.Client` method set for go-ethereum v1.17.5** (2026-11-07).

The external contract is defined only by the
[go-ethereum compatibility policy](go-ethereum-compatibility-policy.md). A
release names one locked go-ethereum version and declares either `complete` or
does not claim compatibility. Internal method families, test batches, and
agent assignments are implementation details and do not create public subset
versions.

EthGuard's `Corroborated` remains provider-backed cross-source evidence, not
Ethereum consensus, canonical proof, or cryptographic finality. 架构收敛仍受
execution、quorum、reference 和 typed 边界约束。

## Current facts

- `go.mod` locks go-ethereum v1.17.5.
- The `EthClient` method-set check currently describes 29 public methods,
  including the lifecycle method. This is an internal inventory checkpoint,
  not a release claim.
- The root `Client` remains the evidence facade. `Client.EthClient()` must
  eventually expose the complete locked-version method set while sharing the
  same runtime, typed projection, transport, and lifecycle implementation.
- Native HTTP and raw WSS use the same transport seam. WSS subscriptions are a
  separate capability and cannot be inferred from unary request/response
  support.
- `internal/runtime` is the sole production composition root;
  `internal/typed` is the sole production typed projection;
  `internal/verification` owns cross-module facade verification.
- The acceptance baseline includes `go test ./... -race -count=1` in addition
  to the ordinary full test run.

## Next work: topology first

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

The topology/document pass is complete when:

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

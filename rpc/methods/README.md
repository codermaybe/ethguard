# RPC Method Policies

This directory holds the Ethereum wire semantics: thin per-method assembly
(`eth_*.go`: constant + `XxxPolicy()` + wiring) over per-object vertical
slices (`receipt.go`, `transaction.go`, `block.go`, `logs.go`); their tests
collect alongside. Shapes used by one method only stay in `eth_*.go` (R1);
slices exist only for objects shared by ≥2 methods or carrying cryptographic
commitments. The comparison whitelists compile at init from the trimmed
`internal/spec/methods.json` (no generated code, no freshness gap); slices own
requiredness. The division against the parent `rpc` package follows one
line: `rpc` owns the `Policy` contract, the `Registry`, and the shared
resolution strategies — everything true of any method and any JSON; this
directory owns Ethereum method semantics — everything that requires knowing
the wire shapes.

That line decides where normalizers live:

- `rpc.CanonicalJSON` is the parent's only normalizer: it is true of any
  JSON and knows nothing about Ethereum.
- Every Ethereum wire shape is canonicalized here. Shapes shared across
  methods collect in `normalize.go` (`normalizeHexQuantity` for every
  quantity-shaped result, `normalizeCanonicalObject` for immutable-content
  object reads); a method-specific shape stays in its method's file
  (`normalizeBlockHead`). Normalizers are unexported, and the 0x/hex
  comparison conventions never leave this directory. The one exception is
  the uint64-shaped height parser: a spelling primitive shared with the
  finalized-head policy and the anchor extraction, kept in
  `rpc.ParseHexQuantity`. Which response fields are quantities at all — the wire
  semantics — still belong to this layer alone.

A policy describes how its method is prepared, normalized, compared,
anchored, and classified. Initial policy groups are:

- deterministic reads;
- block-anchored execution and state reads;
- variable results;
- provider-local observations.

Unknown or unsupported methods never get a file here; they fall through the
registry to transparent forwarding. Default forwarding must produce
`Forwarded`, never `Verified`.

## Standard projection

Method policies own the compile-time standard field declaration. The generic
`rpc` package first produces a source-backed `CommonSet`; this package then
projects it with `ProjectStandard`. Declared paths become core fields,
undeclared common paths become extensions, and missing required paths remain
an explicit method error. OpenRPC is used only as an offline CI reference and
is not loaded by the production projection path.

Projectors are assembled by the client with a method-keyed projector map. A
method policy therefore keeps its existing admission, anchoring, and
corroboration capabilities; adding a projection does not require wrapping or
reimplementing that policy.

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
`Verified`.

Gas estimates, fee observations, and transaction-count reads may have
policies here. Gas optimization, nonce allocation, transaction signing,
broadcast retries, and replacement transactions do not.

# RPC Method Policies

This directory holds exactly one file per verified RPC method
(`eth_chainid.go`, `eth_getblockbyhash.go`); their tests collect in
`methods_test.go`. The division against the parent `rpc` package follows one
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
  `internal/hexqty`. Which response fields are quantities at all — the wire
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

Gas estimates, fee observations, and transaction-count reads may have
policies here. Gas optimization, nonce allocation, transaction signing,
broadcast retries, and replacement transactions do not.

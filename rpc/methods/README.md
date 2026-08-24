# RPC Method Policies

This directory holds exactly one file per verified RPC method
(`eth_chainid.go`, `eth_getblockbyhash.go`) with an adjacent test. Nothing
else lives here: the `Policy` contract, the `Registry`, and the shared
resolution strategies (strict consensus, select-first, transparent
forwarding) belong to the parent `rpc` package.

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

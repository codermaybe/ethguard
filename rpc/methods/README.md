# RPC Method Policies

This directory is reserved for method-aware RPC policies.

Policies describe how a method is normalized, compared, anchored, and
classified. Initial policy groups are:

- deterministic reads;
- block-anchored execution and state reads;
- variable results;
- provider-local observations;
- default forwarding for unknown or unsupported methods.

Default forwarding must produce `Forwarded`, never `Verified`.

Gas estimates, fee observations, and transaction-count reads may have policies
here. Gas optimization, nonce allocation, transaction signing, broadcast
retries, and replacement transactions do not.

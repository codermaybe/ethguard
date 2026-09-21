# EthGuard Domain Language

## RPC client

EthGuard is an RPC client that queries third-party Ethereum execution RPC
sources and returns method-compatible data plus explicit result evidence. It
is not an Ethereum consensus client or a light client.

## Source

A configured third-party RPC endpoint that supplies an observation for a
request. Sources are voting members selected by the caller's configured
membership; matching URLs do not imply independent operators.

## Observation

One source's response to one method request, including its value, error,
latency, and structural or semantic faults.

## Corroboration

Comparison of observations from multiple configured sources under a fixed
membership and a declared support rule. Corroboration is evidence about
cross-source consistency; it is not Ethereum consensus, validator quorum,
canonical membership, or cryptographic finality.

## Compatibility normalization

The method-specific process that accepts permitted wire forms, preserves the
meaning of Ethereum RPC parameters and tags, and produces a comparable view
without silently changing the request's semantics.

## Evidence

The recorded basis for a result: participating sources, support, rejected
observations, faults, reference information, and method-specific validation.
Evidence explains why a result was returned; it does not upgrade provider
reports into protocol proofs.

## Corroborated result

A result that survives the method's compatibility normalization and admission
rules, and is supported by the declared cross-source corroboration policy.
Corroborated means supported by EthGuard's provider evidence; it does not mean
cryptographically proven canonical or final.

## Selected result

A result chosen by a declared selection policy, such as a median or local
observation, without the cross-source agreement required for a corroborated
result. Selection is useful output with a weaker guarantee and must remain
distinct in evidence and status.

## Stable result

A user-facing result that has an explicit status, preserved request semantics,
and enough evidence for the guarantee that status claims. Stability is a
delivery property, not a synonym for Ethereum verification.

## Ethereum consensus

The protocol-level consensus process involving Ethereum's execution and
consensus specifications. This term is reserved for protocol behavior and is
not used to name EthGuard's cross-source comparison.

## Light client

An Ethereum protocol implementation that verifies consensus evidence such as
trusted checkpoints, sync-committee signatures, fork transitions, and Merkle
proofs. EthGuard does not implement this role.

## Moving tag

An Ethereum RPC block tag whose referent can change between requests, such as
`latest`, `safe`, or `finalized`. EthGuard may establish a stable per-call
reference for comparing source observations, while preserving the requested
tag's meaning and explicitly recording that the reference is provider-backed
evidence.

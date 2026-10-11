# RPC Aggregation Design

Status: Historical overview; normative quorum/reference rules live in
`docs/decisions.md` (0025/0028/0029/0032/0036/0050) and
`docs/design/return-value-design.md`. Do not quote this file for default
quorum or anchor-extension behavior.

## Context

Ethereum JSON-RPC responses share a common wire envelope and mostly standard
field meanings, but raw JSON is not a reliable equality relation. Providers can
vary in optional fields, error values, extensions, node freshness, and support
for non-standard methods. Some methods are deterministic only after they are
anchored to a block hash.

EthGuard therefore needs method-aware semantic comparison rather than a
generic JSON equality check.

## Package placement

The design is split across explicit modules:

| Concern | Package |
| --- | --- |
| upstream lifecycle, health, and eligibility | `pool` |
| passive operational observations | `observe` |
| normalized JSON-RPC transport | `rpc` |
| method normalization and comparison | `rpc/methods` |
| semantic quorum, conflict, and evidence | `aggregator` |

`pool` owns execution and returns raw per-source emissions. `aggregator` owns
semantic aggregation, while `rpc/methods` owns Ethereum method semantics.
Neither package owns account or transaction lifecycle state.

## Result classes

### Corroborated

Multiple eligible upstreams agree after normalization and the method policy's
confidence requirement is met.

### Selected

The method is inherently variable or provider-local. A healthy upstream is
selected using policy and health observations, without claiming Ethereum consensus.

### Forwarded

The method is unknown or unsupported by a specialized policy. The request is
passed to one upstream using the low-level RPC transport.

### Conflict

Upstreams return incompatible deterministic results and no policy winner reaches
the required confidence. The caller receives an explicit conflict error.

## Resolution order

```text
1. Reject upstreams failing identity, protocol, or circuit-breaker checks.
2. Ask `pool` to collect raw responses with a shared context and bounded concurrency.
3. Normalize values by Ethereum semantics, not JSON bytes.
4. Cluster semantically equivalent responses.
5. Apply the method policy to choose a winning cluster or an upstream.
6. Record the winning cluster's first captured supporter as Representative;
   Representative is an audit anchor only and does not re-decide Value for
   migrated methods.
7. Return value, quality, block reference, and evidence.
8. Agreement feedback into source scoring stays unwired (see decision 0010).
```

For `latest` and similar moving references, the policy should first establish a
block hash and then perform data reads against that explicit reference when the
method permits it.

## Initial policy groups

| Group | Examples | Policy |
| --- | --- | --- |
| Deterministic | chain ID, historical block, receipt by hash | strict semantic comparison |
| Anchored execution | `eth_call`, logs, state reads | compare after block anchoring |
| Variable | gas price, fee estimates, estimate gas | select or derive a policy value |
| Local observation | pending state, peer count, syncing | upstream-local result |
| Fallback | unknown or extension method | transparent forwarding |

## Initial upstream observations

The pool keeps decaying observations per source:

- availability: successful, failed, and timed-out calls, with a consecutive-
  failure circuit that cools the source down before readmitting a probe;
- latency: an EWMA of successful round-trip times, plus P95/P99 over a
  bounded recent-sample window; HTTP 429 responses are counted distinctly
  inside transport failures;
- tips: block-height samples from low-rate probes backing the freshness
  eligibility gate (lower-median reference, probe-driven recovery).

Identity, malformed responses, severe staleness, and circuit-breaker state are
eligibility gates, not just score inputs. Failures should lower trust quickly;
recovery should be gradual for user traffic and proactively detected by
probes. Semantic agreement feedback ("agreement with a high-confidence
winner") is designed but not yet wired into source scoring.

## Open questions

- What quorum is required for each deterministic policy group?
  (Resolved direction: fixed-membership majority `Q = max(H, floor(N/2)+1)`
  with `H=2` default, see decisions 0028/0029; unanimity survives only as an
  explicit mode. Caller-facing mode/floor surfaces are retired under T07/0050;
  fixed-basis and denominator invariants stay internal.)
- Should evidence be exposed in the public result or only in diagnostics?
- How should provider independence be configured to reduce correlated trust?
- Which methods can be safely transformed from `latest` to an explicit block
  hash? (Partially resolved: the `rpc.Anchored` policy seam plus decision 0007
  answer the mechanism and the anchor's trust rules; the per-method list is
  filled as methods ship — `eth_getBalance` is the reference.)
- Cache-hit provenance for immutable content (does a cached Corroborated stay
  Corroborated?) — prerequisite before any in-process immutability cache.

## Scope limits

Gas estimates, fee data, and transaction counts are RPC observations and may
have method policies. EthGuard does not optimize transaction fees, allocate or
cache nonces, sign transactions, manage transaction queues, retry broadcasts,
or create replacement transactions.

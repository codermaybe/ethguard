# Comparison: OP Stack proxyd vs EthGuard

Status: Reference analysis (proxyd consensus-aware mode, v4.0+,
ethereum-optimism/infra). Facts reflect proxyd's documented behaviour at time
of writing; revisit when either project changes materially.

## Why this document exists

proxyd is the most widely deployed multi-backend RPC router in the Ethereum
ecosystem, which makes its choices a useful mirror for ours. This document
records where production usage validates the architecture record, which capabilities we
deliberately defer, and where the projects diverge because they sit on
opposite sides of a trust boundary.

This is not a ranking. proxyd optimizes availability for an operator-owned,
semi-trusted fleet. EthGuard corroborates responses across independent providers
that may be unreliable or dishonest (see `docs/threat-model.md`).

## What proxyd actually does

proxyd's "consensus" is background fleet curation, not per-request response
comparison:

1. Polls every backend for `latest`, `safe`, `finalized`, peer count, and
   sync state.
2. Resolves a consensus group using the common-ancestor `latest` block plus
   the lowest `safe`/`finalized` blocks across healthy backends.
3. Bans backends whose tip diverges or whose error rate / latency / lag
   exceed thresholds (default ban window 5 minutes).
4. Serves client requests by round-robin single-source forwarding inside the
   consensus group. Requests are never fanned out and compared.
5. Rewrites moving tags before forwarding: a client asking for `latest` gets
   the request rewritten to the resolved consensus block for roughly thirteen
   methods (`eth_call`, `eth_getBalance`, `eth_getLogs`, `eth_getStorageAt`,
   `eth_getTransactionCount`, block queries by number, ...), and the
   `eth_blockNumber` response is overridden with the consensus value.
6. Caches immutable methods (`eth_chainId`, hash-addressed reads) in Redis.

## Trust boundaries and threat models

The decisive difference is who the backends are.

| | proxyd | EthGuard |
| --- | --- | --- |
| Backend assumption | operator-owned, semi-trusted | third-party providers, may be wrong or adversarial |
| Primary adversary | accidental fork, stale node, flaky backend | dishonest or degraded providers; correlated misbehaviour |
| Defense style | pre-flight fleet filtering (health gates + tip agreement) | per-request fan-out, quorum over responses, conflict evidence |
| Client-facing claim | none (availability infrastructure) | explicit status taxonomy with evidence (`Corroborated` implies corroboration) |

Consequence: a Byzantine backend inside proxyd's health gates can serve wrong
answers to arbitrary methods indefinitely as long as its tip data looks sane.
That is out of proxyd's threat model but squarely inside ours. Conversely,
per-request quorum would be over-engineering for an operator who owns every
backend. The two designs are each appropriate to their trust boundary.

## Where proxyd validates the architecture record

- **Tag rewrite ≈ anchored execution group (our P2).** proxyd's rewritten-
  method list is a production-tested answer to "which methods must pin a
  block reference", and it confirms the rewrite belongs in the proxy layer —
  our `Policy.Prepare` seam — rather than in caller code.
- **Ban/unban windows ≈ agreement feedback.** Their time-windowed banning is
  the operational form of "feed semantic agreement back only when confident",
  supporting our decision to keep feedback out of the quorum decision and
  treat it as a separate later mechanism.
- **Cacheable-method list ≈ deterministic policy group.** Independent projects
  converging on the same method partitioning (`eth_chainId`, hash-addressed
  reads) is evidence the grouping in `docs/design/rpc-aggregation.md` is drawn
  along real joints.
- **Health gates ≈ pool eligibility gates.** Eligibility-before-agreement
  matches our pool monitor's role; staleness/lag gates are the natural place
  where future tip tracking will plug in.

## Reorg protection: shipped as anchoring + freshness gating

EthGuard's response-correctness core (quorum over independent sources,
participation-shrinking resistance, conflict evidence) is implemented, and
the reorg/freshness phase projected here has landed in two pieces:

- **Anchored reads (decision 0007):** moving tags are rewritten to a corroborated
  block hash inside `Policy.Prepare` before fan-out — the same primitive as
  proxyd's tag rewrite, but the anchor itself is cross-corroborated first.
  Reference establishment is library-owned (decisions 0026/0036): strict
  latest-head corroboration (default, fail-closed on divergent tips) and
  conservative finalized selection (`WithFinalizedAnchoring`), which adopts
  proxyd's rule of never placing the reference ahead of true finality:
  lowest fresh finalized height, fork-at-height fails closed. The former
  custom-resolver injection surface is retired and returns a migration error;
  it is not a second supported strategy.
- **Tip tracking (0007 amendment):** the pool samples block tips at a low
  rate and quarantines sources trailing a lower-median freshness reference —
  the eligibility-gate form of proxyd's lag thresholds, made collusion-
  resistant so one provider's height lie cannot vote honest peers out.

What remains deliberately different: proxyd curates its fleet in the
background and forwards single-source; EthGuard corroborates every response it
labels.

## Deliberately not borrowed

Redis caching, WebSocket proxying, method whitelisting, auth, and rate
limiting are daemon concerns of a long-running proxy. EthGuard is a library
whose callers own deployment concerns; adding them would grow the security
interface without strengthening any corroboration claim. In-process caching
of immutable content stays deferred until its provenance semantics are
settled (see the accepted decisions and architecture record).

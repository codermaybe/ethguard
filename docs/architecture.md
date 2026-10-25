# Architecture

This file is a hub, not a second record. The frozen module topology and
dependency rules live in
[design/codebase-topology.md](design/codebase-topology.md); the stable data
flow, safety responsibilities, and method coverage strategy live in
[design/architecture-record.md](design/architecture-record.md) — update those
before moving code, not this file.

- Package layout and public surfaces: [../README.md](../README.md) (Layout).
- Accepted behavior and guarantees: [decisions.md](decisions.md).
  Where a decision says "implementation pending", it is the status ledger;
  where code already implements it, the decision's Status line says so.
- Threat model and trust boundary: [threat-model.md](threat-model.md).
- Method semantics: [design/method-contracts.md](design/method-contracts.md).
- Request wire-form evidence: [design/wire-forms.md](design/wire-forms.md).
- Point-in-time baselines (2026-09-07 snapshots, not live status):
  [design/spec-baseline.md](design/spec-baseline.md),
  [design/t00-review.md](design/t00-review.md).

Historical proposals that were superseded stay marked as such inside their
own files (for example `design/ethclient-dropin-architecture.md`); early
drafts under the removed `docs/tmp/` were deleted because they contradict
the accepted record (separate `pkg/consensus`, failure-domain weighting,
zero-dependency claim).

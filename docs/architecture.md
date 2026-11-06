# Architecture

This file is a hub, not a second record. The frozen module topology, dependency
rules, and runtime data flow live in
[design/codebase-topology.md](design/codebase-topology.md). The external
compatibility contract is defined by
[design/go-ethereum-compatibility-policy.md](design/go-ethereum-compatibility-policy.md).
Update those documents before moving code, not this file.

- Package layout and public surfaces: [../README.md](../README.md) (Layout).
- Accepted behavior and guarantees: [decisions.md](decisions.md).
- Threat model and trust boundary: [threat-model.md](threat-model.md).
- Method semantics: [design/method-contracts.md](design/method-contracts.md).
- Current method matrix: [design/method-transport-coverage.md](design/method-transport-coverage.md).
- Release and verification gates: [design/release-contract.md](design/release-contract.md)
  and [design/method-verification-pipeline.md](design/method-verification-pipeline.md).

Historical plans and review snapshots remain available through Git history.

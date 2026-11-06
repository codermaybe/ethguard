# Design documents

This directory contains the current architecture, compatibility policy, runtime
contracts, and executable verification references. It is organized by
ownership rather than by historical work stage.

## Start here

- [Codebase topology](codebase-topology.md) — package ownership, dependency direction, seams, and the runtime pipeline.
- [go-ethereum compatibility policy](go-ethereum-compatibility-policy.md) — the versioned external contract: a complete `ethclient.Client` method set for the locked version.
- [Project roadmap](project-roadmap.md) — current compatibility gaps and topology work.
- [Release contract](release-contract.md) — release gates and evidence rules.
- [Method and transport coverage](method-transport-coverage.md) — the live method matrix.

## Runtime contracts

- [Method contracts](method-contracts.md)
- [Method verification pipeline](method-verification-pipeline.md)
- [Return value design](return-value-design.md)
- [Acceptance thresholds](acceptance-thresholds.md)
- [Reference freshness policy](reference-freshness-policy.md)
- [Moving-tag matrix](moving-tag-matrix.md)
- [Transport contract](transport-contract.md)
- [WSS wire contract](wss-wire-contract.md)
- [Subscription contract](subscription-contract.md)

## Evidence and tooling

- [External RPC test framework](external-rpc-test-framework.md)
- [Capture/golden contract](capture-golden-contract.md)
- [Live canary contract](live-canary-contract.md)
- [Tooling](tooling.md)
- [White-box test ownership](whitebox-test-ownership.md)

Generated method rules remain in this directory because the test suite locks
them byte-for-byte. Stage plans, review snapshots, and superseded proposals
are removed from the active tree; Git history retains them.

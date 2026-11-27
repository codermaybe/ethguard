# Capture plan fixtures

These files use ethguard.rpc-sample-plan/v1. plans_test.go loads and validates them offline; plan loading never performs network I/O and does not prove that a provider capture has run.

- read-local.plan.json: chain identity, network identity, block number, peer count and listening, without block-reference parameters.
- read-volatile.plan.json: gas price, priority fee, blob base fee and syncing. The syncing and syncing-active request IDs distinguish desired false/object cases; actual coverage comes from recorded content, not an ID or synthesized response.

Endpoints come from environment variables and the plan declares chain ID 1. Non-mainnet observations require their own batch/network provenance. Existing HTTP records and the absence of a plan for logs/feeHistory do not close or create provider evidence by themselves.

New moving-tag logs/feeHistory capture must include real reference_context inputs and drive the actual reference path on replay. See [the capture contract](../../../../docs/design/method-verification-pipeline.md#6-捕获与-golden). Missing syncing object or unavailable provider states stay explicit gaps.

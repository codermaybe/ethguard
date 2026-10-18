# Capture plans (stage 0 preparation)

These files are prepared capture plans in `ethguard.rpc-sample-plan/v1`
format. They are loaded and validated offline by `plans_test.go` — loading
never performs network I/O, and no capture has been run yet.

- `read-local.plan.json` — capture-tasks.md 阶段 1 (组 1):
  `eth_chainId`, `net_version`, `eth_blockNumber`, `net_peerCount`,
  `net_listening`. All requests carry no params.
- `read-volatile.plan.json` — capture-tasks.md 阶段 2 (组 2):
  `eth_gasPrice`, `eth_maxPriorityFeePerGas`, `eth_blobBaseFee`,
  `eth_syncing`. `eth_syncing` has two request ids (`syncing`,
  `syncing-active`) because the two response shapes (`false` and the
  synchronizing object) must be observed as separate cases, possibly at two
  different times and two separate batches. If the object state cannot be
  obtained, the coverage gap must be registered as such — never synthesized.

## Boundaries

- Sources are referenced by environment variable (`endpoint_env`) only;
  expected chain ID is 1. A non-mainnet capture would be its own batch with
  its own network context (capture-tasks.md T0.7).
- The `syncing-active` id is a capture placeholder: case identity comes from
  the recorded request id either way.
- 组 3 (`eth_getLogs`, `eth_feeHistory`) has **no plan file yet**: its capture
  may not start until the stage-0 `reference_context` machinery has passed
  audit (capture-tasks.md 阶段 3 gate).

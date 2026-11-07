// Package rpc contains the small transport and decision model shared by the
// pool, method policies, and aggregator. It owns the wire request shape, the
// observation and decision vocabulary, the resolver and decorator building
// blocks, and the execution-plan model; it does not own per-method rules
// (rpc/methods), fleet execution (aggregator), or connection management
// (pool).
//
// File responsibilities:
//
//   - Wire and decision vocabulary: types.go (Request, Observation, Decision),
//     basis.go (frozen voting basis), invocation.go (one logical call's
//     identity and capture), outcome.go (per-source verdicts), errors.go and
//     protocol.go (classified sentinels and protocol errors), result.go
//     (canonical encoding contracts), extensions.go (out-of-projection
//     evidence), reference_request.go (the moving tag a read pins at).
//   - Resolvers: strict.go, median.go, local.go, call_revert.go — the value
//     normalizers and their resolution rules — plus adjudicate.go (support
//     thresholds) and local_observation.go (decision 0035 pending shape).
//   - Execution plan model: read_plan.go, the compiled plan one registered
//     method executes under.
//   - Plan-side helpers: block_reference.go (block-reference spellings and
//     tags shared with the plan model, including the single pending-grammar
//     authority IsPendingReference), acceptance.go (support-threshold seam),
//     representative.go (admitted supporters of a winning cluster),
//     policy.go (Policy interface, optional capabilities, method-rule
//     spelling).
//   - Field aggregation and the write path: field_aggregation.go (JSON
//     path/value evidence) and write.go (eth_sendRawTransaction broadcast).
//   - Shared parsing: quantity.go (0x quantities), error_data_presence.go
//     (JSON-RPC error data completeness), policy.go (Policy interface,
//     optional capabilities, method-rule spelling).
package rpc

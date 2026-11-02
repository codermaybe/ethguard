# 决策实现索引

状态：**半自动索引（2026-11-02）。`docs/decisions.md` 的 Status 行仍是唯一
状态来源；本文件不维护第二份状态台账，只把每条实现声明映射到当前代码的
生产路径、锁定测试和设计文档，供审计与交接使用。**

提取方式：从 `docs/decisions.md` 的 `Status:` 行筛出 `Implemented` 与
`Accepted and implemented` 两种实现声明，逐条到当前树核对后映射。刷新
声明清单用：

```text
awk '/^## /{id=$2; sub(/\..*/,"",id); title=$0; sub(/^## /,"",title)}
     /^Status:/{printf "%s | %s | %s\n", id, title, $0}' docs/decisions.md
```

表格是映射不是语义来源：每条决策的语义以其正文为准，设计文档提供详细
契约。映射漂移（声明命名的符号已不在当前树）列入"待审"，不改动
decisions.md 的状态行。

## 当前实现声明

| 决策 | 声明要点 | 生产路径 | 锁定测试 | 设计文档 |
| --- | --- | --- | --- | --- |
| 0019 | 完整区块内容绑定到被证实的区块身份 | `rpc/internal/objects/block`（`AdmitBlock` 校验 header hash、交易 trie root、withdrawals root、uncle 承诺；`DecodeBlockWithEvidence` 在 typed 边界复检） | `rpc/internal/objects/block/encode_test.go`、`rpc/internal/objects/block/fuzz_test.go` | `method-contracts.md` |
| 0020 | 方法 admission 先于聚类裁决，不可采纳答案保留分母 | `aggregator` 的 admit 阶段（`admitPhase` 在 normalization/clustering 前运行 `AdmissionPolicy.Admit`） | `rpc/methods/admission_suite_test.go`、`internal/verification/integration/field_lifecycle_test.go` | `method-contracts.md`、`return-value-design.md` |
| 0021 / 0034 | Uncertain 按 invocation 捕获不可信原始数据，opt-in 经 invocation context | `uncertain.go`（`WithUncertain`）+ `internal/uncertain` 采集器 | `internal/uncertain/capture_test.go`、`internal/verification/integration/integration_test.go` | `method-contracts.md` |
| 0025 | 一次逻辑 invocation 内冻结投票 basis | `client.go` 经 `internal/refcontext` 在 invocation 开始时快照 corroboration 模式、floor 与 `pool.VotingMembers` | `aggregator/invocation_state_test.go`、`internal/runtime/latest_reference_trace_test.go` | `rpc-aggregation.md`、`method-contracts.md` |
| 0026 | moving reference 每次 invocation 重建，拒绝 legacy 缓存 | `client.go`（`WithAnchorCaching` 保留为显式迁移错误）+ `config`（`anchor_cache_ttl` 非零拒绝） | `config/loader_test.go`、`internal/verification/integration/integration_test.go`（迁移错误两条） | `method-contracts.md` |
| 0028 | 默认 corroboration 为固定成员多数、单位票权 | `rpc.Corroboration`/`rpc.CorroborationShared`（`rpc/adjudicate.go`，mode+floor 校验与原子快照） | `rpc/acceptance_thresholds_test.go`、`rpc/corroboration_composition_test.go`、`rpc/basis_immutability_test.go` | `acceptance-thresholds.md` |
| 0030 | 有意的单源转发模式与受信配置控制 | `client.go`（单 upstream 默认 Forwarded）；`aggregator` 转发收集 | `internal/verification/integration/write_test.go`（`TestSingleSourceClientConstructsForwardedMode`）、`aggregator/composition_test.go`（转发只联系一个源） | `method-contracts.md`、`threat-model.md` |
| 0036 | 库持有的 tag-aware reference 取代自定义 resolver | `anchor.go` 与 `internal/runtime` 的 strict-head/finalized 建立（`WithAnchorResolver` 返回迁移错误） | `internal/runtime/strict_head_equivalence_test.go`、`internal/runtime/latest_reference_trace_test.go` | `reference-policy-proposal.md` |
| 0037 | 保留 Geth block-tag 映射并拒绝启用的 legacy 缓存 | `ethadapter`（-1..-5 到 pending/latest/finalized/safe/earliest） | `ethadapter/block_number_negative_test.go` | `reference-freshness-policy.md` |
| 0038 | corroboration 门保持显式，不引入通用 validity helper | `rpc`（`Decision.IsCorroborated` 仅 corroborated 谓词） | `rpc/decision_test.go`、`aggregator/aggregator_test.go` | `method-verification-pipeline.md` |
| 0062 | Pool 成员构造期固定；health 只管调度门 | `pool.NewWithMembers`（无公开 Join/Remove）、`pool.CollectVoting` 保留全部配置成员 | `pool/membership_test.go`、`pool/voting_test.go` | `adaptive-congestion-control.md`、`reference-freshness-policy.md` |
| 0064 | 显式 raw WSS 与 HTTP 共用统一固定 Source 模型（Accepted and implemented） | `pool.Member.Dialer` 固定绑定、`config` transport 字段、`internal/transport/wss` adapter；0056.2 批准的 raw presence 生产语义经此接线实现 | `unified_source_write_test.go`、`unified_source_presence_test.go`、`config/transport_test.go`、`client_transport_test.go` | `wss-unified-source-write-path.md`、`wss-productionization-decisions.md` |

## 待审（声明与当前树存在漂移）

| 决策 | 漂移 | 当前事实 | 处理建议 |
| --- | --- | --- | --- |
| 0029 | Status 行声称 `config.DefaultSupportFloor` 返回 2，并由 `TestDefaultSupportFloorIsTwo` 锁定；当前树中两者均不存在 | 默认 floor=2 的语义在组合根实现：`client.go` 对多数模式派生 `floor := 2`（Forwarded 为 0），经 `rpc.NewCorroborationShared` 校验；派生默认值已由 `client_corroboration_default_test.go` 锁定（多数模式 floor=2、单源转发无 floor） | 需要主维护者裁决：更新 Status 行指向组合根事实与现有锁定测试，或恢复显式 `config` 默认符号；本索引不代为宣称实现 |

## 历史声明（已被取代，不参与映射）

- **0006** quorum 语义与 participation-shrinking defense：Status 已标注
  被作为默认方向的 0028/0029/0050 取代，正文保留为历史记录；现行分母
  语义见 0028、0050 与上表 0028 行。
- **0027** caller-managed membership：运行时成员操作已被 0062（构造期
  固定成员）取代；现行成员语义见上表 0062 行。

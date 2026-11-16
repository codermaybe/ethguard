# Reference 与 freshness 合同

状态：2026-11-17 文档基线。本文统一 reference 的建立、候选／确认门槛、freshness 作用点及 F1 未决事项。未采纳新的统一默认 lag、样本窗口或请求豁免策略。

## 1. 共同 reference

latest／safe／finalized 分别建立并记录其 provider-reported tag。显式 pin 不换块，失败不降 level、不发业务 fan-out。一次 invocation 内 reference 与业务共享固定成员身份，各轮保留自己的输入和来源处置；不嵌套顶层 Aggregate，不跨调用缓存 reference。

latest／safe 使用 strict head；有 corroboration snapshot 时采用业务 Strict 门槛，无 snapshot 则为底层 legacy 全体一致与单源 Selected，再由 requireCorroborated 门拒绝非 Corroborated。共同参考是 provider-backed 证据，不是链共识或 finality。

## 2. Finalized 候选与确认

| 维度 | 现行算法 |
| --- | --- |
| required | quorum>0 时 `max(2, quorum, floor(len(E)/2)+1)`；quorum=0 时为 len(fresh)，并保留最小 2 限制 |
| E | 第一轮 finalized emissions，包含失败与不可解码来源；失败不缩多数界 |
| fresh | 可解码且通过 lower-median maxLag 过滤的视图；不足 required 或少于 2 则失败 |
| 候选高度 | quorum>0 取第 required 高，quorum=0 取最低 fresh 高度；同候选高度存在不同 hash 先返回冲突 |
| 确认 | 向缺失来源在候选高度补查，沿原第一轮成员集补位；hash 不符形成 reorg 证据，不缩基 |
| 后置检查 | 使用同一冻结 `sel.required`，要求 Support 足够且 Status=Corroborated，不另算第二个门槛 |

业务 Strict 的 N 与 finalized E／fresh 分别承担不同职责，不能因公式相似合并算法。`rpc/acceptance_thresholds_test.go` 与 `internal/reference/finalized_thresholds_test.go` 分别覆盖分母、下限、失败、候选与确认边界；测试存在不表示本次已重跑所有场景。

## 3. Freshness 的两个作用点

| 作用点 | 当前行为 | 不变量 |
| --- | --- | --- |
| pool tip gate | WithTipTracking 采样 tip，以 lower-median staleFloor 门控 CollectVoting 调度 | skipped 成员保留 Basis 与 ErrSourceGated，不自动记 health failure，不换成员 |
| finalized maxLag | 过滤可用候选，再选择和确认 reference | 候选变少可以导致失败，不能降低第一轮多数界；latest／safe 不继承这套 maxLag |

非 corroborating Collect 显式指定 Sources 时按现有内部约定绕过 freshness／cooldown；Broadcast 不做 read freshness gating。当前 gate 按 `TipFreshnessScope` 识别本次请求是否依赖 latest tip（生产业务轮与 reference 轮显式声明 scope），并按 `WithTipSampleMaxAge`（默认三个采样间隔）以 tipObservedAt 判定样本过期——过期样本按未知处理，不证明新鲜或落后。窗口与 scope 仍是调度输入，不是长期产品保证；样本有效性的统一默认值、最低覆盖与结构化原因仍属 F1 未决设计。

## 4. 已接受原则

1. freshness 与 agreement 分离；一个最高 tip 不能独自淘汰其他来源，多数一致也不证明新鲜。
2. gate、故障和候选过滤不减少冻结成员、不改票权、不降门槛、不切 Forwarded。
3. 历史读取不应只因对象旧被拒；pending 不能被改写成已出块状态，具体 gate 范围仍待修。
4. 调度暂停、候选不合格、内容异见和 transport failure 保留可区分原因；freshness 不自动成为健康惩罚。
5. 新的单位、统一默认 lag 或自适应策略需要明确依据和适用范围；不让 pool 建第二套 Ethereum 方法解析器。

## 5. F1：未决设计与验证

| 事项 | 尚需确定／验证 |
| --- | --- |
| 请求范围 | 由 plan／执行轮声明哪些请求适用 gate；显式历史、pending 与无 reference 请求如何处理 |
| 样本有效性 | 过期窗口、停止采样、未知样本、最低覆盖与结构化原因；部分采样的中位数不自动拥有完整 Basis 的多数保证 |
| 单位与默认值 | 块数／时间／组合、chain／方法差异及默认 lag 的依据；与容量控制分开 |
| finalized 配置组合 | runtime 当前 finalized service 使用 majority／floor=2；单源／Forwarded 等组合须通过公开路径裁决与验证，不能宣称所有 preset 等价 |
| 自定义 reader 身份 | Confirm 的陌生成员身份处理属于 seam hardening 候选；普通冻结 reader 未证实可达，不登记为生产漏洞 |
| 证据交付 | 当前失败 reference 通过 refcontext 保留部分证据；是否改为显式结果需证明维护收益并保留失败材料 |

`pool/freshness_scope_test.go` 保存 gate 范围、24h 旧样本和不完整覆盖的现状基线；经裁决修复时应调整现状断言，不能让它们否决新合同。其他入口包括 TestTipTrackingQuarantinesLaggingSource、TestCollectVotingKeepsCoolingMemberInBasis、TestFinalizedAnchoringIsolatesStaleReporter 和 TestBroadcastTrailingSourceStillContacted。

历史 2026-10-31 两轮四源观察约 6分22秒，latest spread 最大1、safe224、finalized96，一个源的 safe/finalized 未变化；没有观察到 reorg 或同高 hash 分歧。这不足以选定阈值。本轮文档整理未重采 provider；后续分别补齐真实更新时间分布、回退／reorg、停止采样、样本过期、请求豁免与确认轮分母证据。

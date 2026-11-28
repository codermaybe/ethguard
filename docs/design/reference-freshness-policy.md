# Reference 与 freshness 合同

状态：2026-11-28 单源业务 Reference 与统一构造入口已实施；2026-11-23 Anchor 三入口专项证据继续有效。本文统一 reference 的建立、候选／确认门槛、freshness 作用点及 F1 未决事项。未采纳新的统一默认 lag、样本窗口或请求豁免策略。

## 1. 共同 reference

默认 strict preset 下，latest／safe／finalized 分别建立对应请求 level 的 reference。显式 pin 不换块，建立失败不切换 level、不发业务 fan-out。一次 invocation 内 reference 与业务共享固定成员身份，各轮保留自己的输入和来源处置；不嵌套顶层 Aggregate，不跨调用缓存 reference。

显式 `WithFinalizedAnchoring` 是现有保守策略例外：default/latest 和 finalized 都进入 finalized 候选／确认算法，safe 仍走 strict safe。这同样适用于 `AnchorBlockHashForTag("latest")` 及依赖该 preset 的 Header 等移动读取，不能将默认 preset 的逐 tag 测试概括成所有配置同义。Reference.Tag 保存请求意图（默认/latest 的内部路径通常为空，ForTag 显式保留入参），不独立证明实际使用的链 level；确认实际来源须结合配置及本轮证据。该 preset 在配置与 Go Option 合并后要求至少两个不同来源，单源组合在建立 pool 前拒绝；普通单源 `finalized` 标签读取不受影响。

独立查询与组合业务读取由同一个 `runtime.ReferenceService`／`ReferenceStage` 建立，root 直接消费证据，不经过额外 Anchor facade。默认 preset 使用 client 构造规则；finalized preset 固定 Majority／Floor=2，包括走 strict 算法的 safe 查询。`rpcengine.ValidateReferenceEvidence` 统一按用途、本次冻结 Basis／Corroboration 与请求 tag 判断可用性。

默认 preset 的 latest／safe／finalized 使用 strict head，始终消费构造规则与本次冻结成员。业务读取仅在 Forwarded／Floor=0、恰好一个冻结来源且 reference 的来源、支持者、门槛、完整 hash 与 tag 均匹配时接受 Forwarded；高度 0 是合法 genesis。多源业务与独立 Anchor 查询仍只接受 Corroborated，单源独立 Anchor 返回 ErrGuaranteeNotMet 并保留失败证据。没有跳过 reference、直接透传 moving tag 的旁路。共同参考是 provider-backed 证据，不是链共识或 finality。

### Anchor 三入口的交付与验收

`AnchorBlockHash`、`AnchorBlockHashForTag`、`AnchorBlockHeight` 是 Client 的独立 query-only 入口，没有对应 EthClient 方法。每次重新建立 reference，无跨调用缓存或 TTL；成功返回的 hash／height 与 `Decision.Reference` 来自同一次建立。顶层 `Decision.Value` 为空，来源、支持者与候选留在 Reference，不复制为业务票。失败保留已形成的部分 Reference，`CallResult.Err` 与返回 error 一致；伴随错误的候选高度不能用作成功锚点。

ForTag 将输入转为小写，仅接受 latest／safe／finalized；其他值本地拒绝且仍发布完成 capture。公共入口每次只发布一次完成，组合读取使用的私有 `establishBusinessReference` 不占用 capture；独立入口的 `establishAnchor` 维持其保证要求。取消、超时及 owner Close 沿共同 operation 保留原因。依赖 reference 的业务读取在建立失败时不发送业务请求，建立成功后的业务失败仍保留该 reference。

2026-11-23 保留运行实现，仅纠正两处策略注释。定向普通测试 facade 10 项、integration 15 项及 root 1 项通过；主控独立 facade／integration race 同范围 25 项通过，integration 另有 11 个子测试，均无失败或跳过。证据包括 `TestAnchorOperationPublishesCompletionOnce`、`TestEstablishAnchorNeverTouchesCapture`、`TestAnchorTransportReferenceMatchesAcrossHTTPAndWSS`、`TestAnchorTransportReestablishesEachCall`、`TestClientAnchorBlockHeightRidesTheEstablishment` 及 reference 失败／生命周期测试。逐 tag 测试固定默认 strict；显式 finalized preset 的例外依现有实现和 preset 证据记录，不代表 F1 的全部配置组合已验收。

## 2. Finalized 候选与确认

| 维度 | 现行算法 |
| --- | --- |
| required | quorum>0 时 `max(2, quorum, floor(len(E)/2)+1)`；quorum=0 时为 len(fresh)，并保留最小 2 限制 |
| E | 第一轮 finalized emissions，包含失败与不可解码来源；失败不缩多数界 |
| fresh | 可解码且通过 lower-median maxLag 过滤的视图；不足 required 或少于 2 则失败 |
| 候选高度 | quorum>0 取第 required 高，quorum=0 取最低 fresh 高度；同候选高度存在不同 hash 先返回冲突 |
| 确认 | 向缺失来源在候选高度补查，沿原第一轮成员集补位；hash 不符形成 reorg 证据，不缩基 |
| 后置检查 | 使用同一冻结 `sel.required`，要求 Support 足够且 Status=Corroborated，不另算第二个门槛 |

业务 Strict 的 N 与 finalized E／fresh 分别承担不同职责，不能因公式相似合并算法。`internal/rpcengine/acceptance_thresholds_test.go` 与 `internal/reference/finalized_thresholds_test.go` 分别覆盖分母、下限、失败、候选与确认边界；测试存在不表示本次已重跑所有场景。

响应模型迁移的证据合同：每个来源保留原`Response`，局部高度不适用以`InterpretationExcluded`表达，不能改写为未回答或连接故障。确认是新的实际交换，完成或失败都替换对应来源槽中的旧响应，不借旧成功内容填补新失败。reference仍拥有自己的选择算法，不把不同轮次的响应累计成额外票数。

`ReferenceEvidence.Sources`交付来源两层摘要，`Adjudication`交付实际门槛及候选成员。候选高度同hash分组发生后，即使因异hash提前返回也保留已形成分组；不足支持、真实异值和后置保证未达成分别记录。上层不能仅凭Hash为空或Status=Unknown就丢弃包含来源/候选的reference。该迁移的全仓验收进度见路线图，F1策略范围保持独立。

## 3. Freshness 的两个作用点

| 作用点 | 当前行为 | 不变量 |
| --- | --- | --- |
| pool tip gate | WithTipTracking 采样 tip，以 lower-median staleFloor 门控 CollectVoting 调度 | skipped 成员保留 Basis 与 ErrSourceGated，不自动记 health failure，不换成员 |
| finalized maxLag | 过滤可用候选，再选择和确认 reference | 候选变少可以导致失败，不能降低第一轮多数界；仅 finalized 算法适用，含显式 finalized preset 的 default/latest；strict 路径不继承 |

非 corroborating Collect 显式指定 Sources 时按现有内部约定绕过 freshness／cooldown；Broadcast 不做 read freshness gating。当前 gate 按 `TipFreshnessScope` 识别本次请求是否依赖 latest tip（生产业务轮与 reference 轮显式声明 scope），并按 `WithTipSampleMaxAge`（默认三个采样间隔）以 tipObservedAt 判定样本过期——过期样本按未知处理，不证明新鲜或落后。窗口与 scope 仍是调度输入，不是长期产品保证；样本有效性的统一默认值、最低覆盖与结构化原因仍属 F1 未决设计。

单源可显式启用 TipTracking，作为健康／RTT／报告高度观察；没有同侪时，自身样本不构成防落后证明。

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
| finalized 配置组合 | finalized service 使用 Majority／Floor=2；单源在构造期拒绝该 preset，普通 finalized 标签保持可用；其他 F1 策略仍独立验证 |
| 自定义 reader 身份 | Confirm 的陌生成员身份处理属于 seam hardening 候选；普通冻结 reader 未证实可达，不登记为生产漏洞 |
| 证据交付 | C 模块已改为 `ReferenceStage.Establish` 显式返回 `(*rpc.ReferenceEvidence, error)`，coordinator 保留部分失败证据并统一交付；`internal/refcontext` 已删除 |

`internal/pool/freshness_scope_test.go` 保存 gate 范围、24h 旧样本和不完整覆盖的现状基线；经裁决修复时应调整现状断言，不能让它们否决新合同。其他入口包括 TestTipTrackingQuarantinesLaggingSource、TestCollectVotingKeepsCoolingMemberInBasis、TestFinalizedAnchoringIsolatesStaleReporter 和 TestBroadcastTrailingSourceStillContacted。

历史 2026-10-31 两轮四源观察约 6分22秒，latest spread 最大1、safe224、finalized96，一个源的 safe/finalized 未变化；没有观察到 reorg 或同高 hash 分歧。这不足以选定阈值。本轮文档整理未重采 provider；后续分别补齐真实更新时间分布、回退／reorg、停止采样、样本过期、请求豁免与确认轮分母证据。

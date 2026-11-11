# Reference freshness：现行边界与实现事实

状态：已接受的不变量与现状说明；**没有采纳新的统一 freshness 策略或默认 lag**。候选方案和待验证项只作本地评审材料，不再在现行合同里保留另一份分阶段提案，也不将未保存的临时记录作为契约入口。

## 两个作用点

| 作用点 | 当前行为 | 边界 |
| --- | --- | --- |
| pool tip gate | `WithTipTracking` 采样 source tip；`CollectVoting` 依据 sampled tip 与 fleet lower-median 的 staleFloor 跳过明显落后的来源 | 被跳过者留在固定 Basis，携带 `ErrSourceGated`，不记 health failure；这是调度，不是换成员或降低门槛 |
| finalized 候选 lag | `internal/reference` 的 finalized 算法用 maxLag 过滤候选视图，再进行选择与确认 | 是候选资格规则；latest/safe strict-head 不采用这套 maxLag；公式见 [acceptance-thresholds.md](acceptance-thresholds.md) |

非 corroborating Collect 若显式指定 Sources，按现行约定跳过 freshness/cooldown gate；Broadcast 不做 read freshness gating。不能把这些内部路径概括成“所有调用都经过同一个 freshness 策略”。

当前 tip gate 不读取本次请求的 tag／height，故显式历史、pending 和无块参数请求也可能少发一个 source；调用本身未因“历史”被直接拒绝，不代表已实现历史读取来源豁免。sampledTip／staleFloor 也没有按 tipObservedAt 年龄过期。这两项是**待裁决缺口，不是认可现状的长期合同**。

## 不变量

1. 保持 tag：latest 不静默变 finalized，safe/finalized 不降为 latest，explicit height/hash 不换块。
2. freshness 与 agreement 分离：一个 provider 的最高高度不能单独证明他源陈旧，多数相同也不证明足够新。
3. 不满足已声明的新鲜度要求应拒绝或跳过，不减少冻结成员／分母、降低 acceptance 门槛、改变票权或切换模式。
4. 显式历史读取原则上不因内容旧而被拒；pending 的节点本地语义不得被 tip gate 改成已出块状态。具体请求范围仍需解决上述实现缺口。
5. 调度暂停、候选不合格、transport failure 与内容异见应有可区分的诊断；freshness 不是自动的 health 惩罚依据。
6. 没有专门决策和真实分布证据，不新增统一“最多落后 N 块／秒”或自适应默认值。
7. provider-reported safe/finalized 的一致性不是 canonicality、独立 finality 或轻客户端证明。

## 现存证据与修改要求

`pool/freshness_scope_test.go` 保存当前请求范围、24h 旧样本与不完整采样覆盖的离线基线；它不是未来行为必须保持的认可。修正行为时应替换对应现状断言，不能让旧测试反过来否决已评审的修复。

其他聚焦证据为 `TestTipTrackingQuarantinesLaggingSource`、`TestCollectVotingKeepsCoolingMemberInBasis`、`TestFinalizedAnchoringIsolatesStaleReporter`、`TestBroadcastTrailingSourceStillContacted`。本次文档清理没有重跑这些测试或重新访问 provider。

2026-10-31 的有限四源观察及仍缺失的 reorg、样本过期、历史请求与确认轮证据见 F1；一次观测不足以选定单位、窗口或阈值。后续方案必须说明其作用点、请求适用范围、样本有效性、固定分母和结构化原因，不能让 pool 拥有第二套 Ethereum 参数解析器。

相关依据：决策 0032／0033／0037／0062；方法级 tag 与 EIP-1898 行为由 [moving-tag-matrix.md](moving-tag-matrix.md) 维护，调度容量控制与内容 freshness 不共享决策语义。

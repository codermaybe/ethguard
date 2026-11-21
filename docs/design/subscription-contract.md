# 订阅合同

状态：2026-11-17 文档基线。本文区分当前公开行为、内部保证与 S1 待裁决设计；不把旧候选 API 或相互冲突的目标语句声明为已实现。WebSocket 协议由 [WSS wire 合同](wss-wire-contract.md)拥有，真实 provider 证据由 [WSS 观察记录](wss-provider-evidence.md)拥有。

## 1. 公开接口与实现范围

Root Client 与 EthClient view 共用以下 channel 与 `ethereum.Subscription` 生命周期接口：

```go
SubscribeNewHead(context.Context, chan<- *ethapi.Header) (ethereum.Subscription, error)
SubscribeFilterLogs(context.Context, ethapi.FilterQuery, chan<- ethapi.Log) (ethereum.Subscription, error)
SubscribeTransactionReceipts(context.Context, *ethapi.TransactionReceiptsQuery, chan<- []ethapi.Receipt) (ethereum.Subscription, error)
```

业务模型已经迁入生成 `ethapi`；ethereum.Subscription 是生命周期接口，不是仍待替换的业务 DTO。root 尚未导出 SubscriptionEvent／RootSubscription 等带裁决事件 API。内部 Delivery、Snapshot 和诊断不能被描述为公开返回类型。调用者提供的业务 channel 不由当前 bridge 关闭；终止由订阅句柄 Err() 及 Unsubscribe 协调。

生产具有单源 channel 和固定 WSS source snapshot 的多源聚合。HTTP request 能力不产生 WSS push 来源；同源 Pull 复用实际来源能力，不拿任意其他 endpoint 代票。当前 multi-source client 只剩一个 WSS 来源时在建立阶段拒绝；Forwarded client 可以走单源路径。公开层没有独立 SingleSourceSelected 配置。

未知 Call 不隐式转发订阅，不把 HTTP polling 叫作原生 WSS 订阅，不承诺 exactly-once、全局顺序、无限补查或 provider 普遍支持。

## 2. 来源、快照与会话

source identity 来自固定配置。一个来源的 push 和 Pull 共用同一身份，每事件最多一票；多个 URL 或多个 transport 不证明底层独立性。连接断开、健康和重拨不修改已建立订阅的成员分母。

实现有两个独立身份：

- 逻辑聚合 snapshot：固定 ID、Members、Quorum 与逻辑 generation；事件上下文消费它，不因一个连接重建而换票基。
- 物理 SourceSession：每来源单调递增 generation，由 SessionGate 检查。迟到 push 或 Pull 须核对原来源及会话，不能在重连后给旧会话记票。

不能把这两个 generation 合称成一个随全局重连变化的数。内部 Snapshot.Generations 等字段的后续命名收敛列入 S1，文档澄清不改变运行时状态。

## 3. 对象检查与事件身份

| 事件 | 当前处理与保证范围 |
| --- | --- |
| newHeads | 共享 Header 内核验证原声明 hash、自算 hash 与适用生成字段；高度、hash、parent 关系用于上下文与候选区分，不能仅按高度合并重组 |
| logs | 共享 Log 内核检查并规范化；block、transaction、index、内容与 removed 共同参与身份／候选，单源重复不增加票数；removed 必须绑定已交付上下文 |
| transactionReceipts | 共享 Receipt 内核、日志／bloom 与批内 block 上下文检查；同 block 的完整规范候选按 index/hash 规范顺序比较，允许过滤子集和空数组，不证明整块收据完整 |

候选指纹来自已检查的规范表示，不直接比较原始 JSON 字符串。数组和 presence 的方法差异保留；非法来源回答不能因为多源同样非法而投递。receipts Pull 保留同源已检查的官方 JSON，不用 geth Receipt 重新编码丢掉字段。查询条件先私有快照，wire 使用生成的 transactionHashes，nil／空查询发送 `{}`。

## 4. 固定门槛与状态机

root 多源聚合固定 `Q=max(2, floor(N/2)+1)`。同一候选获得足够不同 source 票才可交付；只有一个 candidate 完全可以达门槛，candidate 数量不是第二个成功条件。分裂、缺席和失败不缩 N 或 Q。内部 Strict 单源 snapshot 不能达到 Corroborated；内部 SingleSourceSelected 最高 Selected，不等于已有公开选项。

事件状态包括 Collecting、Probing、Quarantined、Decided／Delivered、Revoked 和 Canceled；first-seen、软窗口、硬截止与已交付标记属于事件上下文。软截止可触发 Pull，硬截止不足门槛则 quarantine 并报告错误，不投递未达门槛候选。自适应等待若以后采用只能调整窗口，不能改变成员、权重或保证范围。

同一 source 的接收顺序保留，多源到达先后不成为业务含义；当前不承诺所有高度全局排序。已交付事件不被重新排序，重连不自动重放历史事件。

## 5. 同源 Pull、reorg 与缺口

Pull 在当前事件窗口内向缺失的同源能力补查，保持 source/session 绑定、对象检查和原事件上下文。push/Pull 竞态只能记一票；失败保留故障，不能借别源答案补位。停止订阅必须取消 Pull，迟到结果重新校验物理 session。

newHeads 跳高会在同一来源／会话补查缺失头；成功仅清除 gap 诊断，不重放历史事件。无法补齐通过可 errors.As 为 `SubscriptionGapError` 的终止错误表达。当前验证非阻塞：gap Pull 期间后续事件仍可交付，不承诺阻塞式连续高度。

2026-11-21 新目标（尚未实现）：用户要求执行期间锚点稳定且连续，不能在共同观察100之后静默跳过101而交付102。上述gap行为仅为现状，不能作为新目标的验收标准。缺失高度应进入有来源证据的恢复与裁定，连续推进还须核验父hash衔接；逐高度分别达标不自动形成一条连续路径。缺口未恢复时不得将后续头包装为连续锚点推进。短暂达标观察是否交付、稳定复核条件、重组回退的公开表达和恢复预算仍在设计讨论中；不由本段声明已有完整历史重放或不可逆保证。

logs 与 receipts 不凭通知缺失自行推导高度 gap。logs removed 是显式撤销事实，不能静默删历史；同高不同 hash／parent 的冲突应保留。具体重组完整性和真实 provider 行为仍需证据，不能由局部状态机测试推出。

## 6. 预算与背压

root 当前聚合设置 Queue=256、HardTimeout=30s、MaxPayloadBytes=8MiB、MaxEvidenceBytes=64MiB。内部 Config 要求 queue／hard timeout 为正；Budget 零值可关闭对应 guard。这是当前实现，尚未成为统一公开可配置预算合同。

内部聚合事件队列满时返回 ErrBackpressure 并终止；WSS notification queue 满时由 adapter 返回对应 backpressure 错误。root 向调用者 channel 的发送等待 channel 或 stop，不把它与内部队列策略混称为所有层统一阻塞或统一终止。队列、payload、lifetime budget 是不同限制，不能把 unary WithSourceEvidence 直接扩为无限订阅材料。

## 7. 生命周期与错误的当前事实

| 层／事件 | 当前实现 | 仍需注意 |
| --- | --- | --- |
| adapter stream／内部 aggregator Unsubscribe、Close | 幂等正常终止可以携带 nil；结束内部 event/Err channel | 内部正常终止不推出 root owner Close 一律无错 |
| root 主动 Unsubscribe | 停止 worker、会话、Pull 和交付，等待清理；不重放未知结果 | 早于它的终止错误仍可能已形成 |
| root owner 结束 | 聚合 bridge 使用 owner context cause 结束；单源 bridge 经 stop 清理 | 两条 bridge 的外部错误与完成时序需 S1 统一，不能宣称已完全等价 |
| source disconnect | 多源 worker 记录 fault 并重建该 source session，成员分母保持 | 可恢复 fault channel 当前可丢非阻塞诊断；终态 Err 不能代替持久观测 |
| 无法恢复的对象、quorum、gap、预算或队列错误 | 通过 Err 形成终止结果；root bridge 最多交付一个 terminal error 并关闭 Err | caller 业务 channel 的所有权与内部 event channel 区分 |

建立 context 与 owner 生命周期分别处理：建立取消不能留下半活跃资源，不能用 WithoutCancel 抹掉建立阶段的调用者取消。root 重订阅的指数退避间隔上限为 2s，循环可持续到 stop/context；有界的是间隔，不是重试总次数或总时长。adapter 自身不提供通用重连/replay。

## 8. S1：待裁决设计

| 问题 | 后续需要确定的目标 |
| --- | --- |
| Close 与终止错误 | owner、主动 Unsubscribe、setup/user context、adapter 断连的原因类别和清理／Err 完成顺序；覆盖单源与多源，不在本轮改代码选边 |
| 单源模式 | 是否公开独立 Selected 订阅配置，以及 strict 只有一个 WSS 来源时的建立行为；不暗用 unary Forwarded 代替新合同 |
| 预算与背压 | 对外配置还是固定值，零值规则、每层溢出和终止材料的保留责任 |
| generation | 明确逻辑 snapshot 与物理 session 的命名、归属和 stale 判定，避免重连改写已有事件 |
| 恢复范围 | 退避、尝试次数、总时长和事件窗口分别定义，不互相借用“有界”承诺 |
| 可恢复诊断 | 是否增加非阻塞、低基数、脱敏的计数／Recorder 能力，避免可恢复故障无持久记录 |
| root 事件 API | 是否需要独立裁决事件载体；公开类型、预算和消费者需单独设计，现有 channel API 仍有效 |

## 9. 验证与实现入口

transport demux／notification／取消在 `internal/transport/wss`；固定快照、票数、重复／removed 与队列在 `internal/subscription`；Pull、物理 session、gap、budget 在其相邻文件；root 与 view 入口在根 subscription.go 和 ethclient_view.go，业务模型投影由 internal/typed 负责。

持续覆盖建立失败清理、取消、Unsubscribe、owner Close、断连／重建、迟到 generation、重复／removed／reorg、同源 Pull、quorum、gap、预算／背压和 goroutine 释放。离线、真实本地链与真实 provider 分开记账；WSS raw-call presence 观察不能充当真实订阅观察。当前缺口见 [覆盖清单](method-transport-coverage.md)，本轮没有执行外部订阅采集。

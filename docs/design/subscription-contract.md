# WSS 订阅契约

状态：S0 契约候选已冻结；WSS capability 与 root 聚合已部分实现（2026-11-07）。

本文把 docs/design/tmp/wss-sub-design.md 的探索内容收敛为可审查的订阅契约。订阅 capability 已在 raw-call WSS adapter 旁独立落地，不扩展 pool.Transport。下面的事件、快照、单源、背压和生命周期规则继续约束 root 聚合；公开方法名称仍需 R0/F0 做一次最终签名审查。

当前 internal/transport/wss 是单连接、单 reader、request-ID demux 的 raw-call adapter，并在旁边提供独立的 Subscribe/Unsubscribe capability；root 已接入 newHeads、logs 和 transactionReceipts 的单源/多源路径。订阅仍不能通过未知 Call 或临时通知分支实现；后台 reconnect、重放和写路径恢复也不因此成为通用能力。

## 1. 目标与非目标

订阅目标：

- 对每个配置 source 建立独立的订阅流；
- 把 source 推送转换为带 source identity 和接收时间的内部事件；
- 对相同事件做有界聚合，只投递达到订阅门槛的事件；
- 对可配置且属于同一 source 的 Pull 能力进行补查；
- 明确 reorg、重复、缺口、背压、取消、关闭和异步错误；
- 保持根 Client 的裁决事件切面和 EthClient 的官方 channel 切面独立。

本阶段不目标：

- 不把 raw-call Transport 改成通用 WebSocket 客户端；
- 不自动重放或恢复未知写结果；根聚合器允许按 source 的订阅会话做有界重订阅，
  每次重建递增 generation，迟到帧由 SessionGate 丢弃；transport 层不提供通用
  自动重连或 replay。
- 不改变现有 unary quorum、reference、Decision 或 typed projection；
- 不默认支持单源订阅降级；
- 不把 HTTP 轮询伪装成 WSS 原生订阅；
- 不承诺 exactly-once、全局顺序或不丢事件，除非后续契约和测试明确实现这些语义。

## 2. 两个独立方法切面

### 2.1 EthClient 官方视图

视图遵守锁定 go-ethereum 版本的官方形态：

    SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error)
    SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)

其他官方版本中存在但当前覆盖清单未承诺的方法，必须在方法覆盖文档中标为不支持或待实现，不允许用未知 Call 隐式转发。

视图向 channel 发送的事件只能是已经满足 EthGuard 订阅交付规则的值。异步 transport、quorum、reorg、背压和隔离错误通过 ethereum.Subscription.Err() 交付。视图不暴露裁决对象、source 票数或原始材料。

### 2.2 根 Client 裁决视图

根 Client 不把单次调用的 CallResult 机械复用于流。建议采用独立的事件类型：

    type SubscriptionEvent[T any] struct {
        Value     T
        Decision  *rpc.Decision
        Uncertain *UncertainResult
        EventID   EventID
        Snapshot  SourceSnapshot
    }

根订阅使用独立的事件 channel 和生命周期句柄，不复用 `ResultCapture`：

    type RootSubscription[T any] interface {
        Events() <-chan SubscriptionEvent[T]
        Err() <-chan error
        Unsubscribe()
    }

上面的名称和泛型形状仍是内部候选，S1 可以用非泛型的 newHeads/logs 实现，但必须保留同样的字段和语义。EthClient 只把已经交付的事件转换为官方 channel，并返回 `ethereum.Subscription`；它不暴露 `Decision`、source 票数或原始材料。

事件一旦出现在根订阅 channel 中，Decision.Status 必须表示该事件已经通过订阅门槛。未达成门槛的候选值不能作为普通事件发送。超时、分歧、隔离、连接终止和不可恢复的背压错误通过 Err() 交付。

## 3. Source identity 与快照

“节点完全对等”只表示没有权重、优先级或人工信任等级，不表示没有身份。

每个订阅必须使用稳定的 source identity：

- identity 来自配置的 source/member ID，不从 URL 临时推导；
- 同一 source 的 WSS push 和 Pull adapter 共享一个 identity，只能贡献一票；
- 同一 endpoint 通过不同传输接入时，默认不形成两个独立信任源；
- source 的 URL、transport、订阅能力和 Pull 能力属于配置事实，不进入业务 payload hash。

实现至少需要两个内部标识：

    type SourceSnapshot struct {
        SnapshotID string
        Generation uint64
        Members    []string
        Quorum     int
    }

    type EventID struct {
        Kind       string
        Fingerprint string
        SnapshotID string
        Generation uint64
    }

`Generation` 单调递增且只在一个 source 的连接会话重建时变化；旧 generation 的迟到帧必须在 session 层丢弃。`EventID` 的 fingerprint 使用 canonical typed payload 计算，不能使用原始 JSON 字符串。

一次订阅事件开始时固定 source snapshot：

    members = {source identities eligible when the event context starts}
    quorum  = policy(snapshot.members)

订阅期间 source 上下线、health cooldown 或重拨不能修改已经开始的事件的分母和门槛。新事件可以使用新的 snapshot。当前 unary 的单源 Forwarded 语义不能自动套用到订阅。默认策略为 `Strict`：事件必须达到事件开始时快照的 quorum。只有调用方显式选择 `SingleSourceSelected` 时才允许单源交付；该事件的 `Decision.Status` 固定为 `Selected`，不得标为 `Corroborated`，并在事件诊断中记录降级原因。没有显式选择时，单源 source snapshot 仍可建立连接，但事件保持 `Quarantined`。

`SourceSnapshot` 在订阅创建时分配稳定的 `SnapshotID`，在每个事件首次观察时复制成员集合、generation 和 quorum；连接重拨不会改写已经开始的事件。

## 4. 事件身份与规范化

### 4.1 NewHeads

NewHeads 的聚合 key 至少包含：

- block number；
- block hash；
- parent hash；
- source snapshot generation。

同一 source 对同一事件的重复推送只记一次。仅按 block number 聚合不足以表达 reorg：同一高度的两个 block hash 必须形成不同候选。

投票内容使用经过边界校验的 canonical header 表示。不能直接对 provider 原始 JSON 做字符串比较，也不能因为两个 JSON 字段顺序不同而形成不同候选。header 不完整或无法计算合法 hash 时只能形成 source fault，不能投票。

### 4.2 Logs

日志事件身份至少包含：

- block hash 和 block number；
- transaction hash；
- log index；
- address、topics、data；
- removed 状态。

transaction hash + index 不足以区分 reorg 后的不同 block。内容指纹必须使用规范化的 typed/log 表示，并明确是否把 removed 当作事件类型的一部分。

同一 source 的重复日志只记一次。相同日志在多个 source 达到门槛后只交付一次。一个已交付的日志被 reorg 撤销时，必须交付显式 removed=true 事件或终止错误；不能静默删除历史事件。

### 4.3 事件顺序

当前契约只承诺：

- 单个 source 的接收顺序在内部保留；
- 同一事件的多 source 到达顺序不作为业务语义；
- 不承诺全局 block 顺序，除非订阅模式明确要求按高度排序并实现缺口等待；
- 事件 channel 关闭前，已经交付的事件不会被重新排序。

## 5. Quorum 与状态机

每个事件上下文至少包含：

- 固定 source snapshot；
- first-seen 时间；
- candidate fingerprint 到 source votes；
- 每个 source 的一次投票状态；
- Collecting、Decided、Probing、Quarantined、Canceled 状态；
- 软超时和硬截止时间；
- 已交付/已撤销标记。

只有 candidate 的 source vote 数达到固定 quorum 才能进入 Decided。以下情况不能放行：

- 只有一个 source 的推送；
- 多个 source 但只有一个 candidate；
- 两个 candidate 对半分裂；
- source snapshot 中缺失的 source 未经 Pull 证明；
- 事件内容无法规范化或绑定请求。

软超时可以进入 Probing，但不能改变 quorum。硬截止到期后进入 Quarantined，记录候选、支持者、缺失 source 和 fault，绝不投递未达门槛的 candidate。

自适应跨度只能调整等待窗口，不能调整 source 分母、权重、quorum 或单源降级。软窗口必须始终小于硬截止；如果配置或估算结果违反该关系，应在创建订阅时拒绝或显式 clamp，不能使用负的剩余 duration。

## 6. Push、Pull 与补查

Push 是订阅的主要事件来源。Pull 是事件上下文超时后的同源补查，不是任意 fallback。

每个 source 必须显式声明：

- 是否支持 WSS push；
- 是否有对应 HTTP/WSS request-capable endpoint；
- 哪些事件类型可以 Pull；
- Push 与 Pull 的 request/response 是否共享 source identity；
- Pull 的超时和并发上限。

缺少同源 Pull 能力时，source 只能保持缺失状态，不能借用其他 source 的响应代票。Pull 结果必须经过同一方法的 admission、canonicalization 和 typed binding，再进入原事件上下文。

Push 与 Pull 并发时：

- 同一 source 的结果只记一票；
- Pull 迟到或 Push 迟到都必须幂等；
- Pull 不能创建第二个事件上下文；
- Pull 失败记录 source fault，不改变其他 source 的票数；
- owner context、Unsubscribe 或 Close 取消所有 Pull。

## 7. Reorg、重复与缺口

### Reorg

NewHeads 以 block hash + parent hash 区分候选。若较新高度指向不同 parent，系统必须保留冲突事实，不能仅因为高度连续就覆盖旧事件。

Logs 必须支持 provider 的 removed 语义。收到 removed 事件时，需绑定原 block hash、transaction hash 和 log index；无法绑定的撤销通知进入错误通道，不作为普通日志放行。

### 重复

重复判断分两层：

1. source-local：同一 source、同一 generation、同一事件身份只计一次；
2. delivery-global：同一已决定事件只向业务 channel 发送一次。

重连或未来 generation 不得让旧连接的迟到通知进入新事件上下文。generation 变化需要在内部事件中体现，并由订阅 owner 丢弃旧 generation 的通知。

### 缺口

缺口是观察事实，不自动等于 reorg：

- NewHeads 跳过高度时，记录 gap，并在同一 source/session 的 Pull 窗口逐个验证缺失头；验证成功只清除诊断状态，不重放历史事件；无法补齐则通过 `Err()` 返回可 `errors.As` 为 `SubscriptionGapError` 的终止错误；
- logs 缺少某个 block 的通知时，按同源 filter Pull 能力补查；
- 无法补齐的 newHeads gap 通过 Err 交付结构化终止错误；
- logs 与 transactionReceipts 不从通知自行推导高度 gap；
- 不允许以“后续收到了更高高度”掩盖缺口；
- 当前 root fixed-snapshot 模式采用非阻塞验证：gap Pull 运行期间允许后续事件交付；只有缺口无法补齐时才通过结构化 Err() 终止订阅。若未来增加阻塞模式，必须另立配置与契约。

## 8. 背压、预算与交付

不能使用无界事件队列，也不能在 channel 满时静默丢弃已经通过 quorum 的事件。

订阅至少需要三类独立限制：

- 单事件 payload 上限；
- per-subscription pending/event queue 上限；
- subscription lifetime 的 evidence/material budget。

本轮冻结的策略是：队列满时以可分类的 backpressure error 终止该订阅。实现不得
阻塞 source reader，也不得静默丢弃已经达到 quorum 的事件；终止前已经进入业务
channel 的事件保持有效，尚未交付的候选只留在错误材料中。队列容量、单事件 payload
上限和 lifetime evidence budget 必须由订阅配置提供，任一为零时在建立阶段拒绝。

WithSourceEvidence 当前是单次调用预算，不直接延长为无限订阅预算。订阅若采集原始材料，必须有每事件和总生命周期预算，并保证 snapshot 与队列不会引用调用方的大对象。

## 9. 生命周期与异步错误

订阅生命周期由 owner Client、订阅句柄和调用者 context 共同决定：

| 事件 | 行为 |
| --- | --- |
| 建立 context 取消 | 建立失败，不能创建半活跃订阅 |
| owner Close | 停止所有 source worker、Pull、聚合器和交付 goroutine；关闭事件 channel；Err 返回可识别的关闭错误 |
| Unsubscribe | 幂等；停止新事件；清理 source 订阅、Pull、队列和上下文；不重放未知结果 |
| source disconnect | 记录 source fault；是否终止整个订阅取决于剩余 snapshot 和契约，不自动缩小已开始事件的 quorum |
| quorum timeout | 不发送候选；进入 quarantine/Err 或诊断队列 |
| queue overflow | 不静默丢事件；按明确 backpressure 策略终止或阻塞 |
| terminal error | Err channel 至少交付一次，并最终关闭；事件 channel 只关闭一次 |

Err() 是异步终止事实，不代替已经交付事件的 Decision，也不把 transport error 伪装成 RPC business error。错误必须保留 source、generation、事件身份和阶段信息，便于审计。每个订阅最多发送一个 terminal error，然后关闭 Err channel；`Unsubscribe` 和 owner `Close` 是无错误的正常终止（除非底层 worker 已报告更早的错误）。事件 channel 和 Err channel 都只关闭一次，关闭顺序固定为先停止生产、再关闭事件 channel、最后发送并关闭 Err channel。

### 本轮冻结与最小阻塞项

S1/S2 可以据此实现内部 seam：Strict 是默认单源策略，SingleSourceSelected 是显式
降级，generation 过滤旧帧，队列溢出终止订阅，事件和错误 channel 各关闭一次。仍需
F0 最终确认的只有根 Client 的公开构造函数名称、是否导出 `SubscriptionEvent` 的
类型参数，以及具体配置字段名称；这些都不影响内部事件身份、quorum、Pull、reorg
或生命周期测试。若 F0 不导出根事件切面，EthClient 官方 channel 仍可独立实现，根
事件保持内部测试接口。

## 10. 探索稿中需要修正的实现问题

wss-sub-design.md 的方向可作为输入，但参考代码不能直接执行：

- AdaptiveSpanTracker.GetTimeout 的示例锁操作使用了错误的 unlock 形式；
- hardTimeout - softTimeout 可能为负；
- 输出 channel 满时使用 default 静默丢弃；
- activePullHeader 使用 context.Background()，无法响应 owner Close 或 Unsubscribe；
- 直接保存 *ethclient.Client 会绕过当前 transport/pool/aggregator seam；
- 以高度为唯一 block context key 无法正确表示 reorg；
- 日志 key 缺少 block hash 和 removed 语义；
- cleanup 立即删除高度上下文会让迟到通知重新创建事件并重复交付；
- 代码骨架未定义订阅建立、Unsubscribe、Err channel、队列预算和 source generation；
- 文档第 7 节尚未完成，不能作为已经闭合的架构结论。

## 11. S1/S2 准入条件

S1（WSS 订阅 transport seam）只有在以下条件满足后开始：

- M0 方法/传输清单完成；
- EthClient 官方订阅方法保持 go-ethereum 的 `SubscribeNewHead` 和
  `SubscribeFilterLogs` 形状；根事件先使用内部 `RootSubscription[T]` 候选，公开
  名称和泛型细节留给 F0 一次签名审查；
- source identity、snapshot generation、事件 ID、订阅 Close/Unsubscribe/Err 语义有测试；
- raw-call Transport 不扩张为隐式订阅接口；
- 进程内 WSS server 能验证请求 ID 与通知 demux、断连、取消、Close 和资源回收。

S2（订阅聚合与补查）只有在以下条件满足后开始：

- S1 transport seam 和事件通知接收通过 race；
- newHeads/logs 的 canonicalization、reorg、removed、重复和缺口测试齐全；
- quorum 和单源政策采用本文的 Strict 默认、显式 SingleSourceSelected 降级规则；
- source Pull 能力及同源 identity 映射可验证；
- 背压、per-event/lifetime budget 和异步错误矩阵通过；
- 未决定的 exactly-once、重连和恢复语义不被实现伪造。

## 12. 验收命令与证据

S0 文档验收：

    gofmt -l .
    git diff --check
    go test ./internal/transport/wss -count=1
    go test ./internal/transport/wss -race -count=2

S1/S2 还需：

- WSS request/notification demux contract；
- source disconnect、Unsubscribe、owner Close 和 goroutine leak 检查；
- newHeads/logs reorg、duplicate、gap、quorum、Pull 和 backpressure 矩阵；
- 根 Client 和 EthClient 的独立 API contract；
- 不引入未知 Call fallback，不改变 unary 方法的现有 Decision/typed 语义。

本文的状态只有在上述证据完成后才能从 S0 变为 Accepted.

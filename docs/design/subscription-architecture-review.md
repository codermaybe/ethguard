# S1/S2 订阅架构准入审查

状态：`needs-review`（2026-11-07）。本文把 S0 契约转成可执行的 transport
和聚合准入清单；不实现订阅，不修改 unary `Transport`、`CallResult` 或公共
facade。S1/S2 仍需 R0 冻结公共方法名称、事件结果类型和单源政策后才能进入生产代码。

## 现有边界核对

`internal/transport/wss` 当前是单连接、单 reader、request-ID demux 的
raw-call adapter。`readLoop` 只把带 `id` 的响应交给 pending call；没有 `id`
的通知会被忽略。`CallContext` 的取消只删除自己的 pending 项，`Close` 等待在途
调用或由调用者 context 结束，读失败会有界地失败全部 pending 并关闭连接。现有
`TestNotificationsAreIgnored` 明确把 `eth_subscription` 通知当作 unary 连接上的
无关帧。这个行为必须保持，S1 不能在同一 `Transport` 上偷偷增加通知回调。

WSS wire contract 还明确禁止 batch、subscription、自动重连、重放和写入恢复。
因此 S1 需要新的能力端口和生命周期对象；它可以复用握手、帧编解码和连接层的
底层组件，但不能把订阅消息塞进 `CallContext` 或未知 `Client.Call`。

## S1：transport seam 准入

S1 的内部 seam 应至少分成三层，名称和公开签名交由 R0 决定：

1. **Source session**：一个配置 source 对应一个有稳定 identity 的连接会话，维护
   connection generation、owner context、能力声明和关闭状态。WSS push 与同源 Pull
   adapter 必须引用同一个 identity，不能各自投一票。
2. **Notification stream**：单独的 reader 路径把 JSON-RPC notification 解成带
   `sourceID`、`generation`、接收时间和原始方法名的内部消息；request response
   仍走现有 pending demux。一个 reader 负责按帧顺序读取，但 response 与
   notification 必须经过不同的队列，不能让 notification 占用 request ID。
3. **Subscription handle**：为每个订阅提供幂等 `Unsubscribe`、只读 `Err` 通道和
   owner cancellation。handle 关闭时必须停止 source workers、清理 Pull 和聚合队列，
   最终只关闭一次事件通道和错误通道。

S1 不允许实现以下行为：自动缩小事件 quorum、把单源推送标为 `Corroborated`、
将 HTTP 轮询伪装为原生订阅、重连后自动重放、或在队列满时静默丢弃事件。

### S1 事件与生命周期不变量

- 每个入站 notification 绑定 source identity 和连接 generation；旧 generation
  的迟到通知在 session 层丢弃，不能进入新事件上下文。
- request ID 只用于 response；缺少 ID 的 notification 不得唤醒或删除 pending。
- malformed notification、未知 subscription id、错误方法和超限 payload 进入带阶段
  信息的异步错误路径；不能伪装成某个 unary RPC error。
- owner `Close`、调用 context 取消和 `Unsubscribe` 都必须能取消 reader、Pull、
  aggregator 与 delivery goroutine，并在有界时间内回收。
- source disconnect 不得改变已经开始的事件 snapshot；是否终止整个订阅由固定契约
  决定，不能临时降低门槛。
- source worker、notification queue 和 error queue 都必须有上限；所有终止路径
  要求 goroutine、连接和 channel 的 race 检查。

### S1 最小测试矩阵

进入实现前，至少要有进程内 scripted WSS peer 覆盖：

| 场景 | 必须证明 |
| --- | --- |
| response 与 notification 交错 | response 仍按 ID 正确交付，notification 不污染 pending |
| 同一 source 重复 notification | source-local 去重只记一票 |
| 两个 generation 的迟到帧 | 旧 generation 被丢弃并留下可审计 fault |
| malformed/未知 notification | 订阅收到异步错误，unary 调用行为不变 |
| source disconnect | worker 退出有界，snapshot/quorum 不被静默改写 |
| `Unsubscribe` 并发通知 | 停止新事件，清理连接和 Pull，幂等且不死锁 |
| owner `Close` | 所有订阅终止，事件/Err channel 各关闭一次，无 goroutine 泄漏 |
| context 在建立和运行中取消 | 建立阶段不产生半活跃订阅；运行阶段取消所有子任务 |
| 有界队列溢出 | 按明确 backpressure 策略阻塞或发送分类错误，不静默丢已决定事件 |

这些测试应在 `-race` 下重复运行，并与现有
`go test ./internal/transport/wss -race -count=2`、raw-call contract 和
architecture import 检查一起执行。

## S2：聚合与同源补查准入

S2 只接受已经通过 S1 的内部事件；它不改变 unary quorum 或 reference。每个事件
上下文固定以下字段：

- `snapshotID` 及 source identity 集合；
- 事件类型和规范化的 event key；
- generation、first-seen、soft deadline、hard deadline；
- 每个 source 的一次投票、candidate fingerprint、fault 和 Pull 状态；
- `Collecting → Probing → Decided/Quarantined → Delivered/Revoked` 状态。

### 事件身份

`newHeads` 的 key 至少包括 block number、block hash、parent hash 和 snapshot
generation。同一高度的不同 hash 必须形成两个候选，不能用高度覆盖 reorg。

`logs` 的 key 至少包括 block hash、block number、transaction hash、log index、
address、topics、data 和 `removed`。`transactionHash + logIndex` 不能单独表达
reorg。`removed=true` 必须绑定原日志身份；无法绑定的撤销通知进入错误路径。

所有候选先经过 typed admission 和 canonicalization，再进入投票。JSON 键顺序、
十六进制大小写和 provider 扩展不能制造不同候选。source-local 重复和 delivery-global
重复都必须幂等；一个已交付事件的撤销不能静默删除历史事件。

### quorum、Pull 与缺口

事件开始时固定分母和门槛。单一 source 推送、未达到门槛的冲突候选、无法规范化的
payload 和 snapshot 中未补查的缺失 source 都不能交付。软超时只允许进入 Probing；
同源 Pull 必须使用该 source 已声明的能力和相同 identity，不能借其他 source 代票。
Push/Pull 竞态由 source-local 一票和事件 key 去重；迟到结果不能创建第二个上下文。

高度跳跃是 gap 事实，不自动等于 reorg。是否阻塞后续事件必须由订阅模式冻结；
无法补齐的 gap 通过 `Err()` 或显式诊断交付，不能被后续更高高度掩盖。

### 交付、背压与异步错误

只有达到 quorum 且通过 typed binding 的 candidate 才能进入业务 channel。根 Client
的裁决事件使用独立事件结果类型，不能复用 unary `CallResult`；EthClient 视图才转换
为官方 `ethereum.Subscription` channel。每事件 payload、per-subscription queue 和
lifetime evidence/material 都必须有预算。

队列满时只能阻塞并受 context/deadline 约束，或用可分类 backpressure error 终止；
默认行为不能静默丢弃已决定事件。`Err()` 至少报告一次终止错误并最终关闭，错误需
保留 source、generation、event key 和阶段，区分 transport fault、quorum timeout、
canonicalization failure、gap 和 backpressure。

## 未决项与升级触发器

以下事项仍属于 `needs-review`，不能由 Flash agent 自行决定：

- EthClient 官方订阅方法与根 Client 事件构造签名；
- 单 source 是否允许显式降级，以及该模式的 Decision 状态；
- snapshot generation 的分配时机和重连后的旧帧处理策略；
- newHeads/logs 的 gap 阻塞策略、Pull 超时和并发上限；
- queue overflow 是阻塞还是终止，以及事件/Err channel 的关闭顺序；
- reorg 撤销事件是否要求普通事件 channel 发送 `removed`，或以终止错误结束；
- evidence/material 的事件级和订阅级预算；
- exactly-once、自动重连和恢复未知订阅结果是否进入承诺范围。

出现以下事实必须退回 R0/F0：需要改 unary `Transport` 或 `CallResult`、需要把单源推送
升级成 `Corroborated`、需要跨 source 借票、需要自动重连/重放、需要新增公共错误层级，
或任何 provider-specific 分支改变 canonicalization/quorum。

## 验收命令

本次审查只改文档，执行：

```text
gofmt -l .
git diff --check
go test ./internal/transport/wss -count=1
go test ./internal/transport/wss -race -count=2
```

实现 S1/S2 前还必须补齐上表的 scripted peer、聚合状态机、reorg/removed、Pull、
backpressure、取消、Close 和 goroutine leak 测试；没有这些证据不得把 S1/S2 标为完成。

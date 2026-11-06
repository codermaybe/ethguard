# S2 内部事件聚合状态机

状态：内部 seam 完成，尚未接入公共订阅 facade（2026-11-07）。

`internal/subscription` 提供与 `Client`、`EthClient`、unary `Transport` 和
`CallResult` 无关的最小状态机。创建时固定 source snapshot、generation、成员分母和
quorum；`Submit` 只接受快照中的 source 和当前 generation，同一 source 对同一候选只
记一票。Strict 模式要求固定 quorum，`SingleSourceSelected` 是显式降级并保持
Selected 语义。

NewHeads 的 context key 按高度组织、candidate key 包含 hash 和 parent；Logs 的
context key 包含 block hash、transaction hash 和 index，candidate key 还包含 removed
和完整 typed 字段。因此 reorg 和 removed 不会被高度或 transaction/index 折叠。Canonical key
函数先统一十六进制大小写，再计算稳定摘要。

软超时只把上下文置为 Probing，硬超时置为 Quarantined 并报告 `ErrQuorumTimeout`；不
改变分母或降低门槛。事件队列有界，队列满时以 `ErrBackpressure` 终止状态机，不能
静默丢弃已决定事件。`Unsubscribe`、`Close` 和 context 取消均为有界、幂等操作。

Pull 只提供同源 source/generation 校验函数，未实现任意 endpoint fallback、reconnect、
重放或业务事件 facade。公共根事件类型、EthClient channel 和 Pull 并发策略仍由 F0
统一审查。

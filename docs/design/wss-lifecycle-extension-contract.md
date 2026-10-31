# WSS 生命周期扩展契约

状态：**当前阶段设计契约（2026-10-31），只冻结边界，不实现扩展功能**。

本文承接 [WSS Transport Adapter](wss-transport-adapter.md)，只讨论自动重连、
keepalive 和 backpressure。raw-call、request ID demux、presence、Close 和
Transport Port 的现有语义以 adapter 文档和进程内 contract 为准。

## 1. 当前边界

- `internal/transport/wss` 仍是一条连接、一个 reader、一个 pending 表的 raw-call
  adapter；不实现订阅、batch、自动重连、自动重放或写路径恢复。
- `Close` 拒绝新调用，等待在途调用完成或取消后释放 socket。没有 deadline 且
  provider 不响应的调用可以延迟 socket 释放；直接使用 adapter 的调用方必须提供
  有界 context，pool 的 collect timeout 是通常的上层兜底。
- 任何扩展都必须保持 `pool.Transport` 的错误、取消、来源归属和调用级边界，不能
  把连接状态写入 Decision、比较键、quorum、reference 或 typed projection。

## 2. 自动重连

当前阶段不自动重连。断连、读错误、read limit 和写失败会终止当前 transport，
所有 pending 收到连接失败，新调用被拒绝。

未来若增加重连，必须由 transport 所有者显式管理 transport generation：

1. 旧连接的 pending 全部结束为失败；未知结果不得自动重放；
2. 新连接使用新的 ID 空间或明确的 generation 组合，迟到的旧响应不能交付给新调用；
3. 重连只恢复连接可用性，不改变 source 成员、health、投票分母或门槛；
4. pool 决定 source health 和下一次调用是否重新拨号，adapter 不自行改变成员资格；
5. 写路径、交易广播和未知交易结果必须另立幂等与重放契约，不能借用 read-call
   重连实现。

重连实现的前置证据是：真实 provider 的断连、重连延迟、限流、旧响应和连接身份
行为；进程内 server 只能证明机制，不足以升级生产契约。

## 3. Keepalive

当前 adapter 不主动发送 ping；gorilla 的控制帧响应能力保持开启，调用 deadline
和 reader 错误负责发现失效连接。

若未来启用主动 keepalive，必须满足：

- ping loop 独立于业务调用，不占用 pending 表和业务 request ID；
- pong/read deadline 的失败只产生 transport 生命周期错误，不伪造 RPC error；
- keepalive 失败立即终止该连接并失败 pending，不触发隐式重放；
- 关闭顺序先停止 ping loop，再按 Close/abort 规则释放 socket；
- 频率、pong 超时和空闲回收参数来自显式配置或后续决策，不写成隐含常数；
- 观测记录连接代数、最后 pong 和失败原因，但不进入 Decision 或 resolver。

## 4. Backpressure 与写队列

当前 adapter 没有独立写队列：`writeMu` 保证单写者，调用方 context 负责写期限，
pending 数量由上层并发和 deadline 约束。该模型不丢弃、不重排、不重放请求。

若真实 provider 证据显示写阻塞或 pending 增长需要控制，候选扩展必须先回答：

- 队列上限按连接、source 还是调用类型计算；
- 排队调用是否在进入 pending 前等待，还是立即以明确的容量错误拒绝；
- 排队等待如何受 context deadline 约束，取消如何释放位置；
- 关闭、断连和 read limit 如何同时清理队列与 pending；
- admission/reference/业务调用是否共享预算，且不能改变固定分母；
- backpressure 信号如何进入 observe，而不改变 health、quorum 或 Decision。

在这些问题有证据前，不增加无界队列、优先级、请求丢弃或按方法重排。

## 5. 生命周期矩阵

| 事件 | 新调用 | pending 调用 | socket | 上层语义 |
| --- | --- | --- | --- | --- |
| context cancel/timeout | 不适用 | 只移除自身 pending | 保持可用 | 返回 context error |
| `Close` | 拒绝 | 等待完成或取消 | 最后一个 pending 后关闭 | 不改变已发调用结果 |
| peer disconnect | 拒绝 | 全部失败 | 关闭 | transport failure |
| read limit | 拒绝 | 全部失败 | 关闭且不可复用 | body-limit failure |
| write failure | 拒绝 | 全部失败 | 中止 | transport failure，禁止重放 |
| future reconnect | 由上层显式决定 | 旧代失败 | 新代另建 | 不自动恢复未知结果 |

## 6. 进入实现的证据门

任何扩展实现前必须单独提交设计修订、进程内 contract 和失败路径测试；不得与
resolver、method plan、reference、T11 或决策 0056 混合提交。至少需要：

- 真实 provider capture 或明确的 provider 限制记录；
- race 下的取消、Close、断连、旧响应、队列清理和资源释放证据；
- pool health、source membership、fixed basis 和 error attribution 不变的回归；
- 明确的配置接线和回滚方式；
- 若涉及写路径或未知结果，单独建立决策，不复用本契约。

本文是当前阶段可修订契约。新的 provider 证据、Transport Port 变化或未知结果
问题可以触发后续决策，但不能把本契约解释为永久禁止扩展。

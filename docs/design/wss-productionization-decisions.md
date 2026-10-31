# WSS 生产化决策输入

状态：**决策输入记录（2026-10-31），未接入配置或 runtime**。WSS raw-call
adapter 已实现并有进程内契约与部分真实 provider 证据，但 W4 的 `null` 证据
仍缺，reconnect、keepalive、backpressure 与限流证据也未完成。本文件只把七个
必须单独裁决的问题、当前事实、约束和最小后续动作固定下来；它不是已接受的
生产决策，也不改变任何运行时语义。

## 1. 当前事实与证据等级

- 默认连接 adapter 是 native HTTP（`client.go` 的 `HTTPAdapterNative`），
  Geth compatibility adapter 只经 `gethcompat` 显式进入；
- WSS adapter 位于 `internal/transport/wss`，只做 raw call、`ChainID` 和
  `Close`；未接入配置/runtime；
- 进程内契约覆盖单请求、乱序、取消、超时、断连、read limit、Close、pending
  清理和四态 presence；
- 2026-10-31 的真实 provider capture 观察到 missing、`0x` 和具体 data，
  **`null` 未观察**；一个公开 endpoint 在特定协议错误后以 `1006` 关闭连接；
  限流、read limit、响应超时、迟到响应和 provider 侧重连未触发；
- 详见 [wss-provider-evidence.md](wss-provider-evidence.md)。

## 2. 已冻结的边界

以下边界在本次记录中不重新讨论：

- 不实现 Subscribe、unsubscribe、filter 或 logs streaming；
- 不实现 JSON-RPC batch；
- 不自动重放未知结果；
- 不把 WSS 接入写路径；写路径的重连、幂等和未知交易结果必须另立决策；
- 不把连接状态、generation、close code 或 backpressure 写入 Decision、
  比较键、quorum、reference、typed projection 或 resolver；
- 不把 Geth 的自动重连语义无声复制到新 Adapter；任何重连都必须显式设计；
- 不改变 pool 的固定成员、固定分母、单位权重、mode/floor/threshold 或
  method plan。

架构 interlock 已禁止 WSS 包导入 aggregator、`rpc/methods`、
`internal/runtime`、`internal/reference` 和 `internal/typed`。

## 3. 七个决策

| # | 问题 | 当前事实 | 选项 | 约束/证据门 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 1 | 是否接入 config/runtime | 未接入；native HTTP 是默认 | (a) 继续只作为独立 adapter；(b) 增加显式配置并在 composition root 接线 | 需要 W4 `null` 与生命周期证据；接线前先冻结配置 schema 和组合根契约；不得改变默认 source 行为 | 需要主维护者决策 |
| 2 | WSS source 如何显式选择 adapter | 目前只能通过内部 Dialer 构造；配置没有 transport 字段 | (a) 根据 URL scheme 自动选择；(b) 显式 per-source adapter 字段；(c) 仅组合根/API 注入 | 不改变 pool 成员或方法 plan；不得让上层读取 scheme 或 close code；错误/health 归属必须明确 | 需要主维护者决策 |
| 3 | reconnect 由谁拥有 | adapter 不重连；geth 的自动重连未复制 | (a) pool 拥有 generation 和重拨；(b) dialer 拥有；(c) 更高层调用方拥有 | 旧 pending 必须失败、未知结果不得重放、旧响应不得交付新调用；source 成员/health/分母不变 | 需要主维护者决策 |
| 4 | keepalive 是否启用 | 不主动 ping；gorilla 仍回应 ping；连接存活性由 reader 和调用 deadline 发现 | (a) 不启用；(b) adapter ping loop；(c) pool 层 liveness | ping 必须独立于 pending/业务 ID；pong 失败只能产生 transport 生命周期错误；参数必须显式配置 | 需要主维护者决策 |
| 5 | backpressure 是否需要队列 | 无独立队列；单写锁，调用 context deadline 约束 pending | (a) 保持无队列；(b) bounded queue；(c) 立即容量错误 | 队列上限/等待/取消/关闭清理必须可解释；不得改变分母或方法优先级；真实写阻塞证据是前置 | 需要主维护者决策 |
| 6 | Close、断连、限流和 health 的归属 | Close 等待 pending；peer disconnect 失败 pending；限流是 protocol/HTTP 错误；health 由 pool 记账 | 需要逐项映射 WSS close code、429/限流、read limit 和本地取消 | 连接状态不进入 Decision/quorum/reference/typed；unattempted 不惩罚 source；取消不等于 provider 故障 | 需要主维护者决策 |
| 7 | 写路径是否永远排除 | 是，当前 adapter 没有写方法 | 无建议变更；任何写路径重连/幂等都要新决策 | 不得复用 read-call reconnect；未知写结果不得自动重试 | 已冻结（无需新决策，除非主维护者另立） |

## 4. 如果决定接入 runtime

顺序必须是：

1. 先写配置和组合根契约：source 如何显式选择 adapter、错误/health 归属、
   Close 和取消语义、回滚开关；
2. 再写最小接线：只把一个显式 source 接到 WSS adapter，默认行为不变；
3. 最后补 pool/runtime 集成测试：固定成员、固定分母、reference、method
   plan、typed 和写路径隔离；
4. reconnect、keepalive、backpressure 与 T11 分别提交，不与接线混合；
5. 每一片都保留可回滚开关，并记录 provider 证据等级。

在这些条件满足前，WSS 只作为可替换 adapter 存在，不进入默认 runtime。

## 5. 进入实现前的证据门

- W4：真实 provider 的 `error.data:null`，或主维护者明确接受“null 仍不可知”；
- 生命周期：真实断连、重连延迟、限流、旧响应和连接身份行为；进程内
  server 只能证明机制；
- backpressure：真实写阻塞或 pending 增长证据，且队列设计不改变分母；
- health：close code/限流/超时的分类不得把合法业务失败误记为容量故障；
- 写路径：另立幂等/重放决策，不复用 read-call 契约。

## 6. 影响与回滚

- 本记录不改变生产代码、默认 runtime、pool、aggregator、reference、typed、
  quorum 或写路径；
- 若未来接线，回滚方式是移除显式配置并恢复 native HTTP 默认；不得留下
  第二套 source 选择或 registry；
- 删除本记录不影响现有 WSS adapter 和进程内契约。

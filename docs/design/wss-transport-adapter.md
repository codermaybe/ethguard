# WSS Transport Adapter（W2/W3：最小 raw-call 实现与进程内证据）

状态：**W2/W3 已实现（2026-10-31）**。实现位于
`internal/transport/wss`，只实现 raw call、`ChainID` 和 `Close`；不包含
Subscribe、filter、批量、自动重连或写路径重放。决策 0056 **未**修订，
WSS 四态 golden 继续禁止。

前置证据：[wss-call-layer-feasibility.md](wss-call-layer-feasibility.md)
（W1 通过：自建最小调用层可行，geth 无公开原始 envelope 扩展点）。

## 1. 归属与依赖

| 项 | 值 |
| --- | --- |
| 代码位置 | `internal/transport/wss` |
| 实现 seam | `pool.Transport` / `transport.Dialer` |
| 编解码 | `internal/rpcwire`（与 native HTTP 共用） |
| 帧协议 | `gorilla/websocket`（不复制 RFC6455 实现） |
| 业务裁决 | 无：不导入 aggregator、rpc/methods、runtime、reference、typed |
| 默认 runtime | 未接入；切换 source 到 WSS 是独立配置决策 |

## 2. 调用与生命周期语义

### 2.1 拨号

- 只接受 `ws` / `wss`；其他 scheme 在拨号前拒绝。
- 一次 `DialContext` 建立一条长连接；调用共享该连接。
- 握手超时默认 10s；入站消息上限默认 `limits.MaxBodyBytes`（32 MiB）。
- 不自动重连。geth WSS 的自动重连语义不会随 Adapter 保留，生产切换前
  必须另立契约（见 §6）。

### 2.2 调用

- 请求 ID 由 `rpcwire.IDGen` 生成，单调、并发安全；不引入第三套 ID 语义。
- pending 表以请求 ID 为键；响应帧到达后删除对应项并交付调用方。
- 取消/超时只删除自己的 pending；迟到响应因无 pending 被丢弃，连接保持
  可用。
- 写操作由单一写锁串行化；调用 deadline 作为写 deadline，无 deadline 的
  调用清除前一次遗留的 deadline。
- 写失败视为连接不可用：所有 pending 失败为
  `transport.ErrConnectionClosed`，新调用得到 `wss.ErrTransportClosed`。

### 2.3 帧校验与错误

- 读取 ID 使用流式 decoder，只解析第一个 JSON 值：带有合法前缀和尾随
  垃圾的帧仍能定位到调用方，再由 `rpcwire.DecodeResponse` 判定
  malformed，而不是被 demux 静默丢弃。
- 成功响应、协议错误、malformed 的判定与 native HTTP 完全一致：
  `jsonrpc` 版本、ID 匹配、result/error 互斥、尾随数据均按
  `rpcwire` 契约处理。
- 协议错误对象实现 `rpc.ProtocolError`、`rpc.DataError` 和
  `rpc.ErrorDataPresence`；`missing`、`null`、`0x`、具体 data 在调用层
  可区分。
- 单帧 malformed 只影响该调用：WebSocket 帧边界仍然完整，连接可继续使用。

### 2.4 read limit

- `SetReadLimit(limit)` 限制单条入站消息；超限时 gorilla 返回
  `websocket.ErrReadLimit` 并关闭连接。
- pending 调用失败为 `transport.ErrBodyTooLarge`；连接标记不可用，后续
  调用得到 `wss.ErrTransportClosed`。不保留截断前缀，不尝试重新同步。

### 2.5 断连与 Close

- 读循环退出（对端关闭、网络错误、协议级读失败）时，所有 pending 失败
  为 `transport.ErrConnectionClosed` 包装的错误，新调用被拒绝。
- `Close` 幂等：标记关闭、关闭 socket；pending 通过读循环的 abort 路径
  得到 `wss.ErrTransportClosed`。

**已知偏离**：`internal/transport/transport.go` 的通用注释写着
“calls already in flight are governed by their own context, not by Close”。
单 socket 的 WSS 无法在关闭 socket 后让在途调用继续；本 Adapter 选择
“Close 释放 socket 并使 pending 失败”，因为不这样做就必须让没有 deadline
的调用无限期持有连接。该偏离与 native HTTP 的行为不同，属于需要主维护者
裁决的 seam 语义问题（见 §6）。

## 3. 非目标（明确不实现）

- Subscribe / unsubscribe / filter / logs streaming；
- JSON-RPC batch；
- 自动重连、重放、未知结果恢复；
- 写路径（交易广播）的重连或幂等语义；
- 主动 ping/pong keepalive（gorilla 自动回应 ping，但不主动发送）；
- 通用 Ethereum 客户端方法；
- 任何 Decision、quorum、reference、method plan 或 typed 逻辑。

## 4. 进程内证据

`internal/transport/wss/wss_contract_test.go` 使用进程内 WebSocket server
（`httptest` + gorilla Upgrader）覆盖：

| 场景 | 测试 |
| --- | --- |
| 单请求、原始 result、请求形状、ID 非空 | `TestCallDeliversOneRequestAndTheExactResult` |
| result target（nil / 非指针） | `TestResultTargetContract` |
| missing / null / `0x` / 具体 data / object data | `TestProtocolErrorsKeepErrorDataPresence` |
| malformed 帧只失败该调用，连接继续可用 | `TestMalformedFrameFailsTheCallAndKeepsTheConnection` |
| 乱序响应 demux | `TestOutOfOrderResponsesAreDemultiplexed` |
| 取消清理 pending，连接继续可用 | `TestCancellationClearsPendingAndKeepsTheConnection` |
| read limit → `ErrBodyTooLarge` → 连接关闭 | `TestReadLimitFailsPendingCallsAndClosesTheTransport` |
| Close 幂等、pending 清理、拒绝新调用 | `TestCloseIsIdempotentAndFailsPendingCalls` |
| 对端断连失败 pending，后续调用被拒绝 | `TestPeerDisconnectFailsPendingCalls` |
| 并发调用共享一条连接 | `TestConcurrentCallsShareOneConnection` |
| 通知帧不影响 pending | `TestNotificationsAreIgnored` |
| `ChainID` | `TestChainID` |
| scheme 拒绝 | `TestDialerRejectsNonWebSocketSchemes` |
| 已取消上下文不产生请求 | `TestCanceledContextSendsNoRequest` |
| 不自动重试 | `TestNoAutomaticRetry` |
| 驱动现有 pool seam（identity probe + collect） | `TestPoolCollectOverWSSAdapter` |

验证命令：

```text
gofmt -l internal/transport/wss
go test ./internal/transport/wss -count=1
go test ./internal/transport/wss -race -count=10
go test ./...
go vet ./...
git diff --check
```

W1 原型 `internal/transport/wss_feasibility_test.go` 保留为可行性证据，不
参与生产构建；W2 实现不依赖该原型。

## 5. 对决策 0056 的影响

没有修订。W3 的进程内证据只证明 **raw-call Adapter 能保留四态**，不证明
真实 provider 的 WSS 行为、重连、backpressure 或 golden 可升级。决策 0056
的出口条件（真实 provider/进程内证据都通过）尚未满足，因此：

- WSS 四态 golden 继续禁止；
- HTTP 四态 replay 保持有效；
- `Decision`、quorum、reference、typed 和写路径语义不变。

## 6. 未决问题（需主维护者裁决）

1. **Close 的 in-flight 语义**：WSS 单 socket 无法让在途调用在 Close 后
   继续。当前实现选择 Close 释放 socket 并使 pending 失败；若要保持
   `transport.go` 注释的通用原则，需要另立 WSS 生命周期契约（例如
   “Close 只拒绝新调用，socket 在最后一个 pending 结束后关闭”），并
   定义无 deadline 调用的资源上限。
2. **自动重连**：geth WSS 当前自动重连；WSS Adapter 明确不重连。生产
   切换前必须决定是否保留、如何保留，以及 pool 健康/失败分类。
3. **keepalive**：是否主动 ping、pong 超时与空闲连接回收。当前不主动
   ping，连接存活性由 reader 和调用 deadline 决定。
4. **backpressure / 写队列**：当前无独立队列，依赖调用方并发与 deadline。
   真实 provider 证据显示写阻塞或 pending 增长时再评估。
5. **provider 证据**：真实 WSS provider 的 error envelope、重连、限流和
   消息大小行为尚未采集；不得用进程内样本冒充兼容性结论。
6. **写阻塞中的取消**：取消发生在 `WriteMessage` 已阻塞时，只有调用
   context 的 deadline 能中断写；没有 deadline 的调用依赖 peer/连接失败
   解除阻塞。pool 的 collect timeout 通常提供该 deadline；若直接使用
   Adapter 且无 deadline，调用方需自行保证有界写。是否在 Adapter 内增加
   写超时需要单独决策。

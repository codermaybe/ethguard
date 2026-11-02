# WSS Transport Adapter（W2/W3：最小 raw-call 实现与进程内证据）

状态：**自有 wire 栈已上线（2026-11-03）**。实现位于 `internal/transport/wss`，
HTTP Upgrade、帧协议与连接生命周期由项目自有代码承担（见
[wss-wire-contract.md](wss-wire-contract.md)），只依赖 Go 标准库；JSON-RPC 语义
仍由 `internal/rpcwire` 独占。只实现 raw call、`ChainID` 和 `Close`；不包含
Subscribe、filter、批量、自动重连或写路径重放。0056.2 已批准生产四态保真，
0064 规定统一 Source；WSS provider-backed 四态 golden 继续禁止。
`gorilla/websocket` 已从生产与测试路径移除（2026-11-03），仅作为
go-ethereum 兼容通道的 indirect 依赖残留在模块图中。

W4 真实 provider 证据已采集（2026-10-31）：missing、`0x` 和具体 data 被实际
frame 证实，`null` 未观察；见 [wss-provider-evidence.md](wss-provider-evidence.md)。

前置证据：[wss-call-layer-feasibility.md](wss-call-layer-feasibility.md)
（W1 通过：自建最小调用层可行，geth 无公开原始 envelope 扩展点）。

## 1. 归属与依赖

| 项 | 值 |
| --- | --- |
| 代码位置 | `internal/transport/wss` |
| 实现 seam | `pool.Transport` / `transport.Dialer` |
| 编解码 | `internal/rpcwire`（与 native HTTP 共用） |
| 帧协议 | 自有实现（`websocket_handshake.go` / `websocket_frame.go` / `websocket_connection.go`，仅 Go 标准库；见 [wss-wire-contract.md](wss-wire-contract.md)） |
| 依赖状态 | 无第三方 WebSocket 依赖；go.mod 中 gorilla/websocket 仅作为 go-ethereum 兼容通道的 indirect 残留，默认构建与 WSS 生产构建均不链接 |
| 业务裁决 | 无：不导入 aggregator、rpc/methods、runtime、reference、typed |
| runtime | `config.Upstream.Transport=wss` 显式选择；与 HTTP 共用固定读写 pool；缺省仍 native HTTP |

## 2. 调用与生命周期语义

### 2.1 拨号

- 只接受 `ws` / `wss`；其他 scheme 在拨号前拒绝。
- 一次 `DialContext` 建立一条长连接；调用共享该连接。
- 握手超时默认 10s；入站消息上限默认 `limits.MaxBodyBytes`（32 MiB）。
- 不自动重连。已接受 pool-owned 下一次独立调用惰性重拨；不恢复失败 invocation。

### 2.2 调用

- 请求 ID 由 `rpcwire.IDGen` 生成，单调、并发安全；不引入第三套 ID 语义。
- pending 表以请求 ID 为键；响应帧到达后删除对应项并交付调用方。
- 等待响应时取消/超时只删除自己的 pending；迟到响应因无 pending 被丢弃，连接
  保持可用。等待写锁后会再次检查 context，取消且未写 frame 不关闭健康 socket。
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

- 自有帧层在帧头解析阶段执行单条入站消息上限（帧级 + 重组后 message 级
  双重限制）；超限立即失败并终止连接，不读取剩余 payload、不保留截断
  前缀、不发送 close 帧（截断流不可再同步）。
- pending 调用失败同时匹配 `transport.ErrBodyTooLarge` 与
  `transport.ErrConnectionClosed`，pool 立即丢弃失效 transport，下一次独立调用
  可重拨；直接复用失效 adapter 会得到 `wss.ErrTransportClosed`。

### 2.5 断连与 Close

- 读循环退出（对端关闭、网络错误、协议级读失败）时，所有 pending 失败
  为 `transport.ErrConnectionClosed` 包装的错误，新调用被拒绝。
- `Close` 幂等：标记 closing、拒绝新调用，等待 pending 调用各自完成或取消，
  然后关闭 socket。该语义与 `internal/transport/transport.go` 的 Port
  契约一致；对端断连、读错误和超限仍会立即 abort 全部 pending。

## 3. 非目标（明确不实现）

- Subscribe / unsubscribe / filter / logs streaming；
- JSON-RPC batch；
- 自动重连、重放、未知结果恢复；
- 写路径（交易广播）的重连或幂等语义；
- 主动 ping/pong keepalive（reader 内部自动回应入站 ping，但不主动发送）；
- 通用 Ethereum 客户端方法；
- 任何 Decision、quorum、reference、method plan 或 typed 逻辑。

## 4. 进程内证据

进程内 WebSocket 对端由自有测试栈提供：`internal/transport/wss` 包内测试使用
同包的 raw hijack server + 自有帧编解码（`startRawServer`/`serverFrames`），
pool 与根测试使用 `internal/testkit/wstest`。`wss_contract_test.go` 与
`lifecycle_test.go` 覆盖：

| 场景 | 测试 |
| --- | --- |
| 单请求、原始 result、请求形状、ID 非空 | `TestCallDeliversOneRequestAndTheExactResult` |
| result target（nil / 非指针） | `TestResultTargetContract` |
| missing / null / `0x` / 具体 data / object data | `TestProtocolErrorsKeepErrorDataPresence` |
| malformed 帧只失败该调用，连接继续可用 | `TestMalformedFrameFailsTheCallAndKeepsTheConnection` |
| 乱序响应 demux | `TestOutOfOrderResponsesAreDemultiplexed` |
| 取消清理 pending，连接继续可用 | `TestCancellationClearsPendingAndKeepsTheConnection` |
| read limit → `ErrBodyTooLarge` → 连接关闭 | `TestReadLimitFailsPendingCallsAndClosesTheTransport` |
| Close 幂等、pending 响应/取消后释放、拒绝新调用 | `TestCloseDrainsPendingWithoutAcceptingNewCalls` |
| 对端断连失败全部 pending，后续调用被拒绝 | `TestReadFailureAbortsEveryPendingCall` |
| 并发调用共享一条连接 | `TestConcurrentCallsShareOneConnection` |
| 通知帧不影响 pending | `TestNotificationsAreIgnored` |
| `ChainID` | `TestChainID` |
| scheme 拒绝 | `TestDialerRejectsNonWebSocketSchemes` |
| 已取消上下文不产生请求 | `TestCanceledContextSendsNoRequest` |
| 写 invocation 失败不自动重试 | `TestFailedWriteInvocationIsNeverReplayed` |
| 驱动现有 pool seam（identity probe + collect） | `TestPoolCollectOverWSSAdapter` |
| 等待写锁时取消不发请求、不破坏连接 | `TestCancellationWaitingForWriterDoesNotSendOrAbort` |
| 实际 frame 写失败 abort 其他 pending | `TestFrameWriteFailureAbortsOtherPendingCalls` |
| pool 关联断连逐 emission 记账与后续重拨 | `pool.TestSharedDisconnectCountsAttemptedEmissionsAndRedials` |
| read limit 后下一调用直接重拨 | `pool.TestReadLimitDiscardsClosedTransportBeforeNextInvocation` |

验证命令：

```text
gofmt -l internal/transport/wss
go test ./internal/transport/wss -count=1
go test ./internal/transport/wss -race -count=10
go test ./...
go vet ./...
git diff --check
```

W1 原型 `internal/transport/wss_feasibility_test.go` 与 W6 双实现差分
`websocket_differential_test.go` 已随 gorilla 移除一并删除（2026-11-03）；
其裁决与差分结论记录在 [wss-call-layer-feasibility.md](wss-call-layer-feasibility.md)、
[wss-wire-contract.md](wss-wire-contract.md) 与执行台账 §17，过程证据以
Git 历史为准。

## 5. 对决策 0056 的影响

0056.2 已接受 raw WSS 的生产四态保真；这是有界的生产行为升级，不是 W4 golden
出口通过。进程内证据证明 Adapter 保真，真实 provider capture 仍只观察到
missing、`0x` 和具体 data，`null` 未观察。因此：

- WSS 四态 golden 继续禁止；
- HTTP 四态 replay 保持有效；
- `Decision`、quorum、reference、typed 和写路径语义不变。

详见 [wss-provider-evidence.md](wss-provider-evidence.md)。

## 6. 已接受限制与未来扩展

扩展边界见 [wss-lifecycle-extension-contract.md](wss-lifecycle-extension-contract.md)。

1. **自动重连**：本期不启 adapter 后台重连；维持 pool-owned 下一次独立调用
   惰性重拨，未知结果不自动重放。未来扩展需另立契约。
2. **keepalive**：是否主动 ping、pong 超时与空闲连接回收。当前不主动
   ping，连接存活性由 reader 和调用 deadline 决定。
3. **backpressure / 写队列**：当前无独立队列，依赖调用方并发与 deadline。
   真实 provider 证据显示写阻塞或 pending 增长时再评估。
4. **provider 证据**：真实 WSS provider 的 missing、`0x` 和具体 data 已采集
   （2026-10-31）；`null`、限流、read limit、响应超时和 provider 侧重连仍未
   观察。不得用进程内样本冒充兼容性结论；见
   [wss-provider-evidence.md](wss-provider-evidence.md)。
5. **写路径路由**：已采用统一 Source。`SendRawTransaction` 使用同一 pool 的
   固定成员，WSS 与 HTTP 均参与；不增加只读组合、角色或 WSS 专用写分支。
6. **presence 生产行为**：已接受保留 raw 四态，不降级 unknown；provider-backed
   golden 的证据门不变，见 [生产化决策矩阵](wss-productionization-decisions.md)。
7. **写阻塞中的取消**：取消发生在 `WriteMessage` 已阻塞时，只有调用
   context 的 deadline 能中断写；没有 deadline 的调用依赖 peer/连接失败
   解除阻塞。pool 的 collect timeout 通常提供该 deadline；若直接使用
   Adapter 且无 deadline，调用方需自行保证有界写。是否在 Adapter 内增加
   写超时需要单独决策。

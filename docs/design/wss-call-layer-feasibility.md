# WSS Call-Layer Feasibility（W1：离线审查与进程内验证）

状态：**已完成（2026-10-31）**。结论：W1 通过。EthGuard 自建最小 WSS
raw-call 调用层可行；go-ethereum v1.17.5 没有可在不放弃原始 error envelope
的前提下使用的公开扩展点；自建层的维护成本有界。本文件只做可行性裁决，
不修订决策 0056，也不宣称 WSS presence golden 已恢复。

事实基线：

- [wss-error-data-presence.md](wss-error-data-presence.md)（根因与边界）；
- [transport-contract.md](transport-contract.md)（L1 行为规范）；
- 决策 0056 / 0056.1、0060；
- 进程内验证：`internal/transport/wss_feasibility_test.go`（仅测试原型，
  不是生产实现；该原型已随 gorilla 移除于 2026-11-03 删除，裁决与过程
  证据以 Git 历史与本文件为准）。

## 1. 复核：坍缩点与公开接口边界

在 go-ethereum v1.17.5 中逐点复核：

| 事实 | 位置 |
| --- | --- |
| 每帧解出的 `jsonrpcMessage.Error` 是 `json.RawMessage`，原始字节尚未丢失 | `rpc/json.go:64` |
| 塌缩发生在 `decodeError`：`json.Unmarshal` 进 `jsonError.Data interface{}`，缺失与 `null` 同为 nil | `rpc/json.go:113` |
| `Client.CallContext` 在返回调用方之前执行 `resp.decodeError()` | `rpc/client.go:367` |
| 分发路径把整条消息交给调用方 goroutine，但公开 API 不暴露该消息 | `rpc/client.go:740`、`rpc/handler.go` |
| `ServerCodec` 名字导出，但 `readBatch`/`close`/`peerInfo` 与 `*jsonrpcMessage` 全部未导出，外部包不能实现 | `rpc/types.go:43` |
| `WithWebsocketDialer` 只替换拨号器；`WithWebsocketMessageSizeLimit` 只改读上限 | `rpc/client_opt.go:66` |
| 客户端自带 ping/pong keepalive、读上限与自动重连 | `rpc/websocket.go:38`、`:224`、`:295`、`:320`、`:372` |

结论：**存在“库内可达”的原始字节，但不存在“外部包可取得”的公开 hook**。
`WithWebsocketDialer` 的拨号器返回的是已建立的 `*websocket.Conn`，调用方
无法在不复制 demux 的前提下把帧与 `CallContext` 的某一次调用可靠关联；
逐帧 tee 只能得到无序帧流，仍需自己解析 ID、维护 pending 表，等于自建调用层。

## 2. 三条路线比较

| 维度 | 自建最小 WSS 调用层 | geth 公开扩展点 | fork / 反射未导出类型 |
| --- | --- | --- | --- |
| 能否取得原始 error envelope | 能：每帧即一个 JSON-RPC 响应 body | 不能：所有公开调用路径都经过 `decodeError` | 能，但依赖未导出字段 |
| 请求 ID demux | 自建：单调 ID + pending map | geth 内部拥有，外部不可关联 | geth 内部拥有 |
| 取消 / 超时 | 每次调用从 pending 表移除；连接保持 | geth 已支持 | geth 已支持 |
| read limit | `SetReadLimit`；超限即关闭连接（`websocket.ErrReadLimit`） | `WithWebsocketMessageSizeLimit`，但超限后错误仍被类型化 | 取决于 fork |
| Close / inflight 清理 | 自建：关闭连接并把所有 pending 失败为 `ErrConnectionClosed` | geth 已支持 | geth 已支持 |
| 断连 | reader 退出，pending 全部失败；不自动重连 | geth 自动重连（语义需单独保留） | 取决于 fork |
| presence | 原始帧直接交给 `rpcwire.DecodeResponse`，三态可辨 | 不可辨（unknown） | 可辨但不可维护 |
| 订阅 / filter | 不实现（EthGuard 当前无订阅接口） | geth 已支持，但不在本 seam | 可支持 |
| 维护成本 | 中：连接生命周期 + demux + 有界读 + 错误映射；帧协议由 gorilla 负责 | 低代码、零 presence | 高：与 go.sum 脱钩，随版本失效 |
| 依赖 | `gorilla/websocket`（当前为 raw WSS adapter 的临时直接依赖；此前已在 go-ethereum 依赖图中） | 无新增 | fork |

### 2.1 为什么不选“geth + tee”

公开的 `websocket.Dialer.NetDialContext` 允许包装底层 `net.Conn` 并观察字节，
但该字节流不携带调用身份：

- geth 的请求 ID 生成器与 `CallContext` 的对应关系不公开；
- 并发调用可能使用相同方法名，按方法匹配不可靠；
- 按时间/顺序匹配在重连、乱序和并发下不成立；
- 要可靠关联就必须自己解析请求帧、建立 ID → pending 映射——这已经是自建
  demux，geth 只剩 JSON 解码，收益不足以抵消双重所有权。

因此该变体不作为独立路线，归入“自建调用层需要自己拥有 demux”的证据。

### 2.2 维护成本判断

自建层需要拥有的知识：

1. 一次调用 = 一个 JSON-RPC 请求帧，ID 单调、并发安全；
2. 单 reader goroutine 解析响应帧，按 ID 交付；通知帧忽略；
3. 每个调用的取消从 pending 表移除，迟到响应丢弃；
4. read limit 超限 = 连接不可恢复，pending 得到 body-limit 失败，后续调用
   得到连接关闭；不能继续读取残帧；
5. `Close` 幂等：关闭 socket、终止 reader、所有 pending 失败；
6. 协议错误由 `rpcwire.DecodeResponse` 解析，presence 三态与 HTTP 对齐；
7. 不实现订阅、批量、自动重连、写路径重放；这些另立契约。

帧协议（握手、掩码、分片、控制帧）由 gorilla 负责；EthGuard 不复制
WebSocket 协议实现。上述 1–7 与 `pool.Transport` 的最小能力一一对应，
没有额外业务裁决。

## 3. 责任矩阵（W2 的实现边界）

| 责任 | W2 Adapter | geth 兼容 Adapter | 说明 |
| --- | --- | --- | --- |
| request ID 生成与 demux | 自建 | geth 内部 | EthGuard 的 ID 单调递增，响应按 ID 匹配 |
| 每次调用的取消 / 超时 | 自建 | geth | 取消只移除该 pending，不关闭连接 |
| read limit | 自建（`SetReadLimit`） | geth 选项 | 超限映射 `transport.ErrBodyTooLarge`，连接终止 |
| Close | 自建（幂等） | geth | pending 失败为 `transport.ErrConnectionClosed` |
| inflight 清理 | 自建 | geth | 断连/限流/关闭三条路径都必须清空 pending |
| 断连检测 | reader 退出 | geth 自动重连 | W2 不重连；重连语义另立契约 |
| presence | 自建（原始帧） | 不可得（unknown） | 复用 `rpcwire`，不新增比较键 |
| 订阅 / filter | 不实现 | geth 支持 | 不在当前 Transport seam 内 |
| ping/pong keepalive | W2 不强制 | geth 30s ping / 30s pong | 若需要，作为独立契约追加 |
| 写路径重连 / 未知结果恢复 | 不实现 | geth 部分 | 决策 0049 的边界不变 |

## 4. 进程内最小验证（已通过）

`internal/transport/wss_feasibility_test.go` 以测试原型驱动一个进程内
WebSocket server（`httptest` + gorilla Upgrader），验证了：

- 乱序响应按 ID 正确交付（`TestWSSPrototypeDemuxesOutOfOrderResponses`）；
- 取消只清除自己的 pending，后续调用继续可用
  （`TestWSSPrototypeCancellationClearsPending`）；
- 超过 read limit 的帧触发 `websocket.ErrReadLimit`，被映射为
  `transport.ErrBodyTooLarge`，连接终止且后续调用为
  `transport.ErrConnectionClosed`
  （`TestWSSPrototypeReadLimitFailsTheConnection`）；
- `Close` 使在途调用失败为 `transport.ErrConnectionClosed`，并阻止新调用
  （`TestWSSPrototypeCloseFailsPendingCalls`）；
- 对端断连使 pending 调用失败而不是挂起
  （`TestWSSPrototypeDisconnectFailsPendingCalls`）；
- 原始 error envelope 经 `rpcwire.DecodeResponse` 后，missing / `null` /
  `0x` / 具体 data 四态可区分，且实现 `rpc.ProtocolError`、
  `rpc.DataError`、`rpc.ErrorDataPresence`
  （`TestWSSPrototypePreservesErrorDataPresence`）；
- 通知帧不干扰 pending 表；`ChainID` 走同一条 raw call
  （`TestWSSPrototypeIgnoresNotifications`、`TestWSSPrototypeChainID`）。

以下命令均通过：

```text
gofmt -l internal/transport
go test ./internal/transport -run 'TestWSSPrototype' -count=1
go test ./internal/transport -run 'TestWSSPrototype' -race -count=5
```

验证只覆盖机制可行性，不是 W3 的完整契约证据：没有 provider capture、
没有 golden、没有订阅、没有重连。原型留在测试文件中，生产构建不引用。

## 5. 裁决与后续

W1 通过，允许进入 W2：实现 EthGuard 专用 WSS raw-call Adapter。

边界（W2 开工前即冻结）：

1. 只实现 raw call + `ChainID` + `Close`，复用 `rpcwire` 编解码；
2. 不实现 Subscribe、filter、批量、自动重连、写路径重放；
3. WSS 代码位于 `internal/transport/wss/`，不进入 resolver、method plan、
   quorum、reference 或 typed；
4. 单请求、乱序、取消、超时、断连、bounded message、Close 和 pending 清理
   必须由进程内契约测试覆盖后，才进入 W3 的四态 golden 讨论；
5. `gorilla/websocket` 从间接依赖提升为 raw WSS Adapter 的临时直接依赖；它只
   服务 `internal/transport/wss`，不构成公共 API。WSS adapter 替换或退出时
   重新评估并移除；版本升级随 transport seam 一起审查。

停止条件：如果 W2 的实现需要复制订阅、重连或通用客户端语义才能通过现有
`pool.Transport`，立即停止在 W1，不进入 W3。

## 6. 未决问题

- ping/pong keepalive：W2 是否维持空闲连接的主动探活；当前判断是“首个
  Adapter 不主动 ping，连接存活性由 reader 与调用 deadline 决定”，需要
  在 W2 契约测试中固定，或另立 keepalive 契约；
- geth WSS 的自动重连语义：替换 transport 时不得无声丢失；W2 明确不重连，
  但生产切换前必须有独立决策；
- backpressure / 写队列：当前`pool` 的调用并发与超时足以约束 pending 数量，
  首个 Adapter 不引入独立队列；如果真实 provider 证据显示写阻塞，再评估；
- 是否把 `rpcwire` 的请求 ID 生成器暴露给 WSS Adapter（当前计划复用
  `rpcwire.IDGen`，不新增第三套 ID 语义）。

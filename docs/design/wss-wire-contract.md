# WSS Wire Contract（自有 WebSocket 底层契约）

状态：2026-11-21 unary两层响应出口已实施；现有 wire 范围起于 2026-11-03。自有实现的握手、帧、连接与
adapter 文件已存在；本文不再保留完成阶段的逐提交计划，也不据此宣称所有验收门
或依赖清理均已完成。

本文件拥有 WSS wire、连接生命周期和实现范围；JSON-RPC 语义由
`internal/rpcwire` 独占。订阅聚合由 [subscription-contract.md](subscription-contract.md)
拥有；真实 provider 的 dated observations 与未观察项由
[wss-provider-evidence.md](wss-provider-evidence.md) 拥有，不能反向扩大兼容性承诺。

## 0. 边界总纲

- **实现 request/response raw-call 与 opt-in notification stream。** 一条长连接、一个
  reader、一个 pending 表、一个写锁；请求 ID demux；订阅使用独立的有界队列和
  subscription ID 绑定。订阅生命周期、聚合和错误边界见
  [subscription-contract.md](subscription-contract.md)。不支持 batch、自动重连或重放。
- **不自下而上重写传输。** 连接建立与字节流使用 Go 标准库（`net`、`crypto/tls`、
  `net/http`、`bufio`）；帧协议与调用生命周期由项目自有代码实现。
- **响应事实归属。** unary Port使用`Call(...) rpc.Response`，保留Exchange及Answer；
  RPC信封仍由rpcwire解释，方法准入及quorum仍由上层负责。中立关闭和大小上限哨兵保持。
  只有关联到当前请求的完整消息才能形成该请求的回答；未知ID和通知不能冒充它。
  取消时保留已经关联的回答，最终错误仍由context决定；它不取消其他调用或污染socket。
- **依赖规则。** 自有生产 wire 层只依赖 Go 标准库与项目内部包
  （`internal/rpcwire`、`internal/limits`、`internal/transport`、`rpc`），
  不得导入或链接第三方 WebSocket 库。`dependencies_test.go` 约束 WSS 包的
  import/link 边界，`gorilla_interop_test.go` 只作为测试 peer。默认构建不链接
  `ethclient` 的既有架构门不变。**范围区别**：`go.mod` 仍列有 gorilla，
  不能把“自有 WSS 不链接 gorilla”写成“整个模块已移除 gorilla”；全模块依赖
  去留不是本次文档整理的行为决定。

## 1. 分层与文件

| 层 | 文件（`internal/transport/wss/`） | 职责 |
| --- | --- | --- |
| 握手 | `websocket_handshake.go` | HTTP Upgrade、ws/wss 拨号、TLS、101 校验、Sec-WebSocket-Accept、deadline/取消 |
| 帧编解码 | `websocket_frame.go` | 帧头、mask、opcode/RSV、fragmentation、UTF-8、close code、双重限制 |
| 连接与生命周期 | `websocket_connection.go` | 单 reader、控制帧自处理、写串行化、read limit 分类、关闭语义 |
| adapter 绑定 | `wss.go` | `transport.Dialer`/`Transport` 表面、pending demux、presence 透传 |

分层保持独立；不以阶段编号、migration 文件或临时并行 wire 栈代替职责划分。

## 2. 支持范围（明确支持）

### 2.1 握手层

- `ws` → 明文 TCP；`wss` → `crypto/tls`（系统根证书、校验服务器证书与
  server name；`InsecureSkipVerify` 恒为 false）。其他 scheme 拨号前拒绝。
- 客户端 HTTP Upgrade 请求：`GET {path}{query} HTTP/1.1`、`Host`、
  `Upgrade: websocket`、`Connection: Upgrade`、`Sec-WebSocket-Key`（每连接
  随机 16 字节）、`Sec-WebSocket-Version: 13`。不请求子协议，不请求扩展。
- 响应校验 fail closed：101 Switching Protocols 之外的状态码（含 3xx 重定向）
  一律拒绝；`Upgrade`/`Connection` 头错误拒绝；`Sec-WebSocket-Accept` 与
  `base64(SHA1(key + GUID))` 不符拒绝；响应携带 `Sec-WebSocket-Extensions`
  （未协商）或未请求的 `Sec-WebSocket-Protocol` 拒绝。
- 握手 deadline 默认 10s（现 adapter 值）；context 取消即时中止拨号、TLS
  握手与响应读取。TLS 证书错误原样归类为握手失败，不改写为 transport 失败。
- 拨号 context 只约束握手阶段；成功返回前解除其取消回调。返回后取消
  拨号 context（包括 Pool 清理单次拨号的派生 context）不得关闭已交付的
  共享连接；握手期间取消仍关闭底层 socket，中止 TCP/TLS/Upgrade I/O。

### 2.2 帧层（RFC6455）

- 基本帧头 2 字节；7-bit / 16-bit / 64-bit payload length；64-bit 长度的
  最高位必须为 0 且不得超出读上限（溢出即 fail closed）。
- 客户端出站帧必须 mask（随机 4 字节 key，每帧重新生成）；入站帧必须未
  mask（RFC6455 §5.1：server→client 不得 mask，收到 masked 帧按协议错误
  关闭）。
- opcode：text(0x1)、binary(0x2)、continuation(0x0)、close(0x8)、ping(0x9)、
  pong(0xA)；未知 opcode 与 RSV1/2/3 非零（无扩展协商）拒绝。
- fragmented message：FIN=0 的 data 帧后接 continuation 序列；control 帧
  不允许分片且 payload ≤ 125 字节。
- text 消息（含分片重组后的完整消息）必须为合法 UTF-8；跨分片边界用增量
  校验，失败按协议错误关闭（close code 1007）。
- close 帧：2 字节 code + UTF-8 reason；code 校验合法接收集合
  （1000–1003、1007–1011、1012–1014、3000–4999；1004/1005/1006/1015 及
  其他 1016–2999 非法）。截断 close 帧按协议错误分类。
- 双重限制：单帧 payload 受读上限约束，重组后的完整 message 同样受读上限
  约束；超限立即失败并终止连接，不保留截断前缀，不把截断字节交给
  `rpcwire`。
- malformed 帧（坏头、坏 mask 位、坏 opcode、坏 continuation、control 分片、
  64-bit 溢出、UTF-8 错误、超限）→ 对应 close code（1002/1007/1009）发出
  close 帧后终止连接。

### 2.3 连接与生命周期层

连接与生命周期语义由本节和 `transport-contract.md` 共同拥有：

- 一个 reader goroutine 拥有全部入站解析；ping 自动回 pong、close 自动回
  close，不占用业务 pending 表与业务 request ID。
- request ID demux、等待响应时取消只移除自身 pending、等待写锁时取消不发
  帧、写失败终止连接、读失败/read limit 失败全部 pending、Close 拒绝新调用
  但不取消在途 unary 调用、旧响应不交付新调用、不自动重放。`Close()` 本身
  不等待在途调用返回；socket 在最后一个 pending 完成/取消后释放，读写故障
  仍可立即 abort（`wss.go` 的 `Close`、`finishCloseAfterPending`、`abort`）。
- Close 清空未绑定订阅的 notification 缓存及淘汰顺序；进入 closing 或 closed 后，
  reader 不再缓存或分发 notification。在途 unary 响应仍正常交付，最后一个调用
  完成或取消后才释放 socket；不得因等待在途响应而重建 notification 缓存。
- read limit 超限的 pending 失败同时保留 `transport.ErrBodyTooLarge` 与
  `transport.ErrConnectionClosed` 两个哨兵（现 `classifyReadError` 语义）。
- ChainID admission 失败后坏连接被丢弃、下一次独立调用才允许惰性重拨：
  pool 侧语义不变，adapter 只负责让失败可分类。
- health、basis、fault 与 write outcome 语义不变；WSS 与 HTTP 的错误解释
  统一规则不变。

## 3. 明确不实现

- 通用 WebSocket API；订阅能力仅限当前 transport/subscription 契约定义的
  `Subscribe`、`Unsubscribe` 和 notification stream；
- JSON-RPC batch；
- compression / permessage-deflate 及任何扩展协商；
- 任意消息类型、并发 reader、事件回调等通用客户端行为；
- 自动重连、自动 replay、未知交易结果恢复（决策 0056.2 与生命周期契约继续
  禁止；pool-owned 下一次独立调用惰性重拨不变）；
- provider-specific close/reconnect 行为适配；
- 主动 keepalive、独立写队列/backpressure、连接代数：须另立设计与验收，
  不因已有订阅有界队列就成为 wire 层的通用能力。

## 4. 持续验收边界

| 边界 | 已有源码入口（测试存在不等于本次已执行） |
| --- | --- |
| 生产 wire 不依赖第三方 WS/geth client、不得反向导入 aggregator/rpc/methods/runtime/reference/typed | `dependencies_test.go` 与架构 interlock |
| Upgrade、TLS、非 101、坏头、超时/取消、重定向/未协商扩展拒绝 | `websocket_handshake_test.go`、`websocket_handshake_ipv6_test.go` |
| RFC6455、malformed、长度/UTF-8/read limit、分片 | `websocket_frame_test.go`、`websocket_frame_fuzz_test.go` |
| 单 reader、控制帧、写串行化与连接失败 | `websocket_connection_test.go`、`write_wedge_test.go` |
| pending demux、取消、Close、不重放、presence 与通知隔离 | `wss_contract_test.go`、`lifecycle_test.go`、`notification_test.go`、`notification_pending_test.go` |
| 外部实现互操作 | `gorilla_interop_test.go`；测试 peer 不能充当真实 provider 证据 |

## 5. 不变量清单

1. 协议错误实现 `rpc.ProtocolError`、`rpc.DataError`、`rpc.ErrorDataPresence`，
   data 原始字节逐字保留（missing/null/value；`0x` 仍是 present value）。
2. malformed JSON-RPC 响应只失败该调用，帧边界完整时连接继续可用；wire
   协议错误或 read limit 则终止连接，不混同两类失败。
3. 取消/超时不损坏健康连接；写路径 invocation 失败不重试、不重放。
4. 连接终止后新调用得到 `wss.ErrTransportClosed`；其与中立关闭哨兵的关系
   保持既有 adapter 契约。
5. 不新增第二套 adapter registry；`config.TransportWSS` 的显式组合根 dispatch
   不变。pool 下一次独立调用的惰性重拨与根订阅 session 重建各自有 owner，
   都不是 transport 自动 replay。

## 6. 待后续实现阶段评估

写阶段取消原因与部分写入后的 abort、TCP／TLS／Upgrade 的共同握手预算、Subscribe setup deadline 所有权、畸形通知的连接级影响与 ReceivedAt 的接收／交付含义，以及 direct dial／proxy 范围仍需逐项设计。旧探针没有在本轮重跑，不能直接登记为已复现缺陷。source session 重建、逻辑snapshot与最终错误由订阅owner负责，wire不据此增加自动replay。

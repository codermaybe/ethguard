# WSS Wire Contract（自有 WebSocket 底层实现范围冻结）

状态：**设计冻结（W0，2026-11-03）。本文件冻结 EthGuard 自有 WSS 底层的范围、
分层与验收；后续 W1–W7 的每个提交只实现其中一层，不扩张本契约。**

本文件是 WSS wire、连接生命周期和当前实现边界的唯一正文。目标：把
`gorilla/websocket` 从生产依赖中移除，由 EthGuard 自有
代码掌握 HTTP Upgrade、WebSocket 帧协议和调用生命周期；JSON-RPC 语义继续由
`internal/rpcwire` 独占。

## 0. 边界总纲

- **只实现 request/response raw-call。** 一条长连接、一个 reader、一个 pending
  表、一个写锁；请求 ID demux；无订阅、无 batch、无重连、无重放。该形态与
  当前 transport/subscription 契约的已接受边界一致。
- **不自下而上重写传输。** 连接建立与字节流使用 Go 标准库（`net`、`crypto/tls`、
  `net/http`、`bufio`）；帧协议与调用生命周期由项目自有代码实现。
- **不改变上层语义。** 不修改 `pool`、`aggregator`、`ReadPlan`、quorum、
  reference、Decision、presence 或 typed projection。`transport.Transport`
  Port 与中立哨兵（`ErrConnectionClosed`、`ErrBodyTooLarge`）保持不变。
- **依赖规则。** 新 wire 层只依赖 Go 标准库与项目内部包（`internal/rpcwire`、
  `internal/limits`、`internal/transport`、`rpc`）。`gorilla/websocket` 在过渡期
  仅允许作为对照测试实现存在（server-side 测试 peer、差分验证 oracle），不得
  进入任何新生产文件；W7 移除后 go.mod 不再出现该直接依赖。默认构建不链接
  `ethclient` 的既有架构门不变。

## 1. 分层与文件

| 层 | 文件（`internal/transport/wss/`） | 职责 |
| --- | --- | --- |
| 握手 | `websocket_handshake.go` | HTTP Upgrade、ws/wss 拨号、TLS、101 校验、Sec-WebSocket-Accept、deadline/取消 |
| 帧编解码 | `websocket_frame.go` | 帧头、mask、opcode/RSV、fragmentation、UTF-8、close code、双重限制 |
| 连接与生命周期 | `websocket_connection.go` | 单 reader、控制帧自处理、写串行化、read limit 分类、关闭语义 |
| adapter 绑定 | `wss.go`（现有文件，W5 改造） | `transport.Dialer`/`Transport` 表面、pending demux、presence 透传 |

每层单独提交、单独验收；禁止 `wss_phase_N.go`、migration 文件或跨层混合提交。

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
  并等待在途调用、旧响应不交付新调用、不自动重放——逐条保持现有契约测试
  的断言不变。
- read limit 超限的 pending 失败同时保留 `transport.ErrBodyTooLarge` 与
  `transport.ErrConnectionClosed` 两个哨兵（现 `classifyReadError` 语义）。
- ChainID admission 失败后坏连接被丢弃、下一次独立调用才允许惰性重拨：
  pool 侧语义不变，adapter 只负责让失败可分类。
- health、basis、fault 与 write outcome 语义不变；WSS 与 HTTP 的错误解释
  统一规则不变。

## 3. 明确不实现

- subscription / unsubscribe / filter / logs streaming；
- JSON-RPC batch；
- compression / permessage-deflate 及任何扩展协商；
- 通用 WebSocket API（任意消息类型、并发 reader、事件回调等）；
- 自动重连、自动 replay、未知交易结果恢复（决策 0056.2 与生命周期契约继续
  禁止；pool-owned 下一次独立调用惰性重拨不变）；
- provider-specific close/reconnect 行为适配；
- 主动 keepalive、写队列/backpressure、连接代数——进入 W8 前不启动，且每种
  能力须单独设计、单独提交、单独验收，不得混入 wire 替换提交。

## 4. 交付与验收序列

| 阶段 | 交付 | 验收焦点 |
| --- | --- | --- |
| W0 | 本文件 | 与现有契约一致；不动 pool/aggregator/ReadPlan/quorum |
| W1 | wire 层依赖清单 + 架构互锁测试 | 生产 wire 文件不导入第三方 WS 包；过渡期 gorilla 仅测试可见 |
| W2 | 握手层 + 测试 | loopback 握手、TLS 错误、非 101、坏头、超时/取消、拒绝重定向/压缩/未知扩展 |
| W3 | 帧编解码 + 测试 + fuzz | RFC6455 基础向量、malformed 全类、64-bit 溢出、UTF-8、read limit、race |
| W4 | 连接生命周期 + 测试 | 现有契约全表保持（取消、写失败、断连、Close、presence、不重放） |
| W5 | adapter 换用自有栈 | 现有 WSS contract、mixed HTTP/WSS、presence 四态、ChainID/read limit/Close 全部通过 |
| W6 | 双实现差分 | 同一 fixture 经自有与 gorilla 对照实现，差异逐条解释；race/fuzz/重复运行 |
| W7 | 移除 gorilla | `go list -deps` 无 gorilla；全量 test/race/vet/architecture 门通过 |

W7 验收命令（目标：默认生产构建与 WSS 生产构建都不再引用 gorilla）：

```text
go list -deps . | grep -E 'gorilla/websocket|go-ethereum/(rpc|ethclient)'   # 仅允许 gethcompat 图内的既有命中
go list -deps ./internal/transport/wss ./cmd/wsscapture | grep gorilla      # 必须为空
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
go test ./internal/transport/wss -race -count=10
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
```

## 5. 不变量清单（W1–W7 每个提交都不得破坏）

1. `internal/transport/wss` 不导入 aggregator、rpc/methods、runtime、
   reference、typed（既有架构互锁测试继续生效）；
2. 协议错误实现 `rpc.ProtocolError`、`rpc.DataError`、`rpc.ErrorDataPresence`
   且 data 原始字节逐字保留（missing/null/value 三态）；
3. malformed 响应只失败该调用，帧边界完整时连接继续可用；
4. 取消/超时不损坏健康连接，已发调用的结果不被重置；
5. 写路径 invocation 失败不重试、不重放；连接终止后新调用得到
   `wss.ErrTransportClosed`；
6. 不新增第二套 adapter registry；`config.TransportWSS` 显式选择的组合根
   dispatch 不变；
7. 每个提交单一业务意图，可用 `go test ./... -count=1` 复核。

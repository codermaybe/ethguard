# HTTP/WSS connection 与 RPC transport seam 迁移设计

状态：**事实审计完成，方案待分片实现**（2026-10-25）

本文是决策 0060 的执行设计。它只处理 pool 如何连接和发起一次 RPC 请求，
不重新定义方法 policy、quorum、Decision、reference、typed projection 或写路径。

## 1. 当前事实

当前调用链为：

```text
Pool
  → Source.clientForCall
  → internal dialer
  → *ethclient.Client
  → client.Client().CallContext
  → pool.Emission{Val, Err}
  → aggregator / reference / policy
```

当前已有 `pool.Transport`，但它只包住了进入 `Source` 后的 client：

```go
type Transport interface {
    CallContext(context.Context, any, string, ...any) error
    ChainID(context.Context) (*big.Int, error)
    Close()
}
```

实际依赖仍在 seam 外侧：

- `pool.dialer` 返回具体 `*ethclient.Client`；
- `standardDialer` 使用 `ethclient.DialContext`，由 Geth 的 `ethereum/rpc` 选择
  HTTP/WSS codec；
- HTTP 的 `capturingTransport` 通过 Geth `DialOptions` 接入，只能在 HTTP body 层
  恢复 error data presence；
- WSS 经过 Geth `decodeError` 后，缺失 `error.data` 与显式 JSON `null` 都成为
  `ErrorData() == nil`；
- `CallContext` 只向上返回 error，raw error envelope 和 presence 没有独立的
  transport 结果类型。

此外，HTTP body capture、Geth client wrapper 和 error-data 恢复目前仍位于
`pool` 包内。它们属于 connection Adapter 的实现，不应成为 pool 调度模块的
协议知识；这是进入自有 HTTP/WSS 实现之前必须先修正的模块归属问题。

因此，当前风险是“Transport Port 名义存在，但连接和 RPC 调用的 Adapter 仍被
第三方 client 类型和错误解码绑定”。这不是未固定版本风险：`go.mod` 已固定
go-ethereum v1.17.5；风险是版本升级、HTTP/WSS 行为和 `ethereum/rpc` 内部语义
变化会直接影响 pool 的证据输入。

### go-ethereum v1.17.5 的行为清单

这次审计还确认了一个不能在自有 WSS Adapter 中顺手删掉的语义：HTTP 与 WSS
在 Geth client 内部不是同一种生命周期。

- HTTP client 建立时不发起网络连接；每次 `CallContext` 都通过带 context 的
  `http.Client.Do` 发出独立 POST。HTTP 非 2xx 会变成带状态码和 body 的
  `HTTPError`，JSON-RPC envelope 再由 client 解码。HTTP client 的 `Close` 是
  no-op，正在进行的请求由请求 context 结束。
- WSS client 建立 dispatch/read/write goroutine，按 JSON-RPC ID 管理 pending
  calls，设置 32 MiB 默认读取上限，维护 ping/pong，并在写失败后自动重连；重连
  后会把尚未完成的最后一个 request 重新登记到新连接。`Close` 会等待 dispatch
  停止并结束 pending calls。
- Geth 的 WSS `decodeError` 只保留解码后的 `interface{}`，因此缺失
  `error.data` 与显式 JSON `null` 最终都表现为 nil；当前 HTTP capture 是额外
  的 pool 逻辑，WSS 没有对应的 raw presence 通道。

因此，“只实现一次请求/一次响应、没有重连”的自有 WSS Adapter 不能直接作为
当前 Geth WSS 的替换品：它会改变写失败、pending request、断线和未知写结果的
语义。除非另立连接失败契约并完成全量差分验收，否则 Slice 3 不得切换生产路径。

## 2. 不可改变的边界

迁移必须保持：

1. `Pool`/`Source` 仍拥有连接生命周期、lazy dial、关闭、健康、调度和 source identity；
2. `pool.Transport` 是唯一连接调用 seam，HTTP 和 WSS 都是它的 Adapter；
3. `pool.Emission` 仍携带 raw result 或 source-attributed error，pool 不做 quorum、
   admission、comparison 或 reference 选择；
4. `aggregator`、`reference`、`rpc/methods`、typed projection 和 Decision 不读取
   URL scheme，也不认识 HTTP/WSS 具体类型；
5. `ethadapter` 继续保持 ethclient-shaped API 和 Geth public types；
6. 不创建平行 Block/Receipt/Transaction 类型、hex codec、consensus encoding 或
   第二套 client 语义；
7. WSS presence 保真不自动改变 `eth_call` data 维度的裁决行为；在决策 0056 升级前，
   missing/null 仍按未知 presence 表达，不生成 WSS 四态 golden；
8. 交易广播、subscription、filter、自动重连、自动重放和未知结果恢复另立契约。

### 边界裁决

“pool 负责多 RPC 管理和建立连接”这个方向是正确的，但“建立连接”应理解为
**持有 Source、调用注入的 Dialer、复用 Transport、关闭 Transport**，而不是在
pool 内实现 HTTP、WebSocket 或 Geth RPC codec。网络协议和第三方 client 都应在
Adapter 内部完成。

目标依赖方向应保持为：

```text
HTTP/WSS/Geth/custom Adapter
          ↓ satisfies
      Transport/Dialer seam
          ↓ used by
          pool
          ↓ emits
       raw Emission
          ↓ consumed by
   aggregator orchestration
          ↓ converts to
      rpc.Observation
          ↓ resolved by
  pure rpc policy / decision logic
```

因此，`ethclient` 与自有 client、Geth WebSocket 与自有 WebSocket 可以并列存在，
但它们是同一个 `Dialer` 选择点上的 Adapter，不应由 pool 同时理解或互相回退。
切换策略、fallback、灰度和 provider-specific capability 应由 composition root
或独立的 Adapter factory 决定。

当前 `aggregator.Aggregator` 还同时承担 collection orchestration 和 decision
resolution；最终纯逻辑目标应是把接收 `pool.Emission` 的编排层与接收
`rpc.Observation` 的 policy/resolution 层分开。这个拆分不属于本次 transport
relocation 的机械片，但必须保持为后续架构债务，不能把更多连接知识继续塞进
aggregator。

## 3. 最小迁移形状

第一步不改 `Transport` 的业务能力面，而是把具体 Geth client 隐藏到 dial Adapter：

```go
type dialer interface {
    DialContext(context.Context, string) (Transport, error)
}
```

现有 `gethTransport` 变成一个兼容 Adapter，负责把 `*ethclient.Client` 填入当前
`Transport`。这样第一片只移动所有权，不改变请求、错误、健康或关闭语义。

`Transport.CallContext` 继续使用 result 目标和 JSON-RPC method/params 形状，避免在
第一片同时改变 pool、aggregator 和测试 Adapter。raw error presence 不作为新的
Transport Interface 方法强制所有实现提供；它通过可选的 error capability 传递：

```go
type ErrorDataPresence interface {
    ErrorDataJSON() (json.RawMessage, bool)
}
```

没有该 capability 的旧 Geth Adapter 保持现有未知 presence 行为；HTTP/WSS 自有
Adapter 在取得保真 envelope 后提供它。这样可在不改变 `Emission`、`Observation`
和普通错误构造的前提下渐进增加诊断能力。是否把 presence 提升到 `rpc.RPCError`
字段，必须另立兼容性设计，不能在 transport slice 中顺手修改。

`ChainID` 暂时保留在 Transport Port：它是 Source identity admission 所需的最小
操作，不把 chain identity 的请求组装散落到 Source。未来若所有 Adapter 都能通过
`CallContext` 以相同方式实现，再单独评估是否缩小该 Interface。

## 4. 分片顺序

### Slice 0：冻结 contract 和基线

- 记录 `Transport`、dialer、Source close、context cancellation、health accounting、
  `ErrClientQuit`/`ErrClosed` 映射和 optional `ErrorDataPresence` 的契约；
- 为 fake Transport 增加 close、cancel、chain-ID、JSON-RPC error 和乱序无关的单请求
  验证；
- 不改变生产 Adapter。

出口：当前全量、pool、aggregator、reference、T09 和 race 通过，且测试可以不依赖
具体 `*ethclient.Client` 驱动 Source → aggregator 路径。

### Slice 1：Geth compatibility Adapter

- 将内部 dialer 的返回类型改为 `Transport`；
- `standardDialer` 继续使用 Geth v1.17.5，包装为 `gethTransport`；
- 将 HTTP capturing path 的测试改为验证 Adapter，而非让 Source 知道 Geth client；
- 保持当前 HTTP presence preserving、body limit、context precedence、Close 和
  health 行为。

这是低风险结构片，不是替换 Geth。它先证明 Transport seam 真的拥有连接实现。

### Slice 1b：Adapter relocation（当前下一步）

先把现有行为完整搬到独立的 `internal/transport` 模块，不改变任何 wire 或错误
语义：

- `internal/transport` 持有中性的 `Transport`、`Dialer` 契约，以及 Geth
  compatibility Adapter；
- HTTP body capture、`preservedDataError`、body limit 和 raw error presence 恢复
  全部归入该模块；
- `pool` 只依赖中性契约，`collectOne` 不再创建 raw-body box 或解析 HTTP body；
- 为兼容现有调用，`pool.Transport` 可以暂时保留为中性契约的 type alias，
  但 pool 不再持有 `ethclient.Client`、`ethereum/rpc` 或 HTTP 实现；
- pool 的默认构造只选择一个 Dialer Adapter，不把协议实现重新放回 pool；
- 原有 HTTP/WSS、body limit、context precedence、health、Close 和 error 分类测试
  必须原样通过，新增的 pool contract tests 继续保持与 Geth 无关。
- pool 的健康判断和 aggregator 的 protocol-error normalization 不能继续直接
  依赖 Geth `rpc.Error`；Slice 1c 将其改为项目拥有的最小接口，Adapter 负责把
  Geth/custom 错误映射到该接口。

这是模块归属片，不是自有客户端片。若迁移导致任何错误分类、Close 阻塞、WSS
重连或 raw presence 变化，必须停止并回滚该片。

### Slice 1c：中性 protocol-error capability

Adapter relocation 之后，继续去除生产路径对 `go-ethereum/rpc.Error` 和
`DataError` 的结构依赖：

- 在项目自己的 `rpc` domain 包中定义最小的 `ProtocolError`、data capability 和
  rate-limit capability；
- pool、aggregator、write 和 livecanary 只依赖这些 capability，不直接导入
  Geth RPC error interface；
- Geth Adapter 通过现有 error chain 和 wrapper 实现这些 capability，自有 HTTP/WSS
  Adapter 也只需实现项目契约；
- `rpc.RPCError` 仍是 observation/policy 使用的结构化值，不把它强行变成 error，
  不改变 `Decision`、fault、revert 或 write rejection 语义；
- Geth-specific test fixtures 只保留在 Adapter 测试，domain/aggregator 测试使用
  project-owned fake errors。

该片完成后，替换连接 Adapter 不需要让上层认识第三方 error 类型；它仍不涉及
自有 HTTP/WSS client、自动重连或 error presence 四态裁决。

### Slice 1d：HTTP behavior differential audit（当前下一出口）

这一片只冻结当前 Geth HTTP compatibility Adapter 的可观察基线，不实现自有 client：

- 建立 request envelope、ID/params、result、JSON-RPC error、HTTP status、body
  limit、malformed/empty response、cancel/deadline、connection reuse、concurrency
  和 Close 的测试矩阵；
- 对每个场景记录 result/error、`errors.Is`、protocol capability、health accounting
  和 raw error presence 的可观察结果；
- 标明“必须兼容的现有语义”“Geth 私有实现”“pool 不承诺的行为”和“需主维护者
  裁决的行为”；
- 不改变 `Transport`、`Emission`、`Observation`、`RPCError`、Decision 或健康分类，
  不引入自有 HTTP/WSS 实现。

出口：差分矩阵完成并经主维护者审查；所有未决语义均有明确裁决或被明确排除。

### Slice 2：自有 HTTP request/response Adapter（审计通过后单独评估）

HTTP 自有 Adapter 需要自行承担：连接池、request ID、JSON-RPC envelope、context
取消、response body 上限、HTTP status、structured error、error data presence 和
Close。它必须与 Slice 1 做差分测试，覆盖成功、JSON-RPC error、HTTP error、空 body、
malformed JSON、超限、取消、连接复用和关闭。

在差分结果稳定前，不删除 Geth HTTP Adapter，也不把自有实现写成更强的标准实现。

### Slice 3：WSS request/response Adapter（HTTP 稳定后，当前不授权切换）

WSS 的最低审计清单仍包括：

- request ID demux 和乱序响应；
- context cancellation；
- 有界 message；
- error envelope presence（缺失、null、具体 data）；
- malformed frame、错误 ID、断连、超时和 Close 时 pending call 清理；
- 明确 ping/pong 与读写 goroutine 生命周期；并额外决定是否保留 Geth 的写失败
  自动重连和 pending request 重登记。

subscription 和 filter 不属于当前 `Transport` 接口，可以排除在 Adapter 之外；但
自动重连、自动重放和未知写结果恢复不能无声排除，因为 Geth WSS 当前已经提供了
其中一部分行为。只有完成行为决策、差分测试和 race 验收后，才评估是否切换；再
评估决策 0056 和加入 WSS 四态 golden。

### Slice 4：可选的依赖收窄

只有 HTTP/WSS Adapter 和所有现有 gates 稳定后，才评估进一步减少 pool 对
go-ethereum `ethclient`/`ethereum/rpc` 的直接依赖。该片必须分别量化：

- 传输和连接生命周期；
- wire error 与 raw presence；
- WebSocket 行为；
- Geth Ethereum types 的兼容面；
- cryptographic/commitment 类型；
- 测试 Adapter、replay 和 live evidence fixture。

完整移除 go-ethereum、建立平行 Ethereum types 或大爆炸重写不在授权范围内。

## 5. 风险与代价

| 风险 | 影响 | 控制方式 |
| --- | --- | --- |
| 自有 HTTP client 与 Geth 行为不一致 | 请求、错误、超时和连接复用漂移 | 先保留 Geth Adapter，差分测试后再切换 |
| WSS demux/cancellation 竞态 | pending call 泄漏、错误响应归属错误 | 乱序、取消、关闭、断连和 race 测试先行 |
| error presence 进入业务语义 | 比较键、Decision、replay 和 T09 语义扩散 | 先作为可选诊断 capability，不改裁决 |
| dialer 改签名影响测试设施 | pool fake dialer 和 rawbody 测试大范围变化 | Slice 0/1 单独提交，保留 geth compatibility Adapter |
| Geth 版本升级改变私有实现 | HTTP/WSS 行为和 typed 结果漂移 | 固定版本基线，升级前跑版本差异与全量 gates |
| 连接 seam 过宽 | 复刻 `ethclient.Client`，失去 Depth | 只承载一次调用、ChainID 和 Close |
| 过早支持 subscription/filter | 引入跨调用状态和新证据语义 | 另立契约，不混入请求/响应 Adapter |

## 6. 推荐决策

Slice 0/1/1b/1c 已完成。当前只授权 **Slice 1d：HTTP behavior differential audit**；
不实现自有 HTTP/WSS client，不改变 `rpc.RPCError`、`Emission`、`Observation` 或
Decision。

Slice 1d 通过后，由主维护者单独审查 HTTP 行为差分和 WSS 重连语义；没有这两项审查，
不进入自有 HTTP/WSS 实现。这样替换风险被隔离在独立的 Transport Adapter，
而不会同时改变方法、聚合、reference、typed 或公开 adapter 语义。

## 7. 执行记录

- `2642551`：Slice 0，加入不依赖 `ethclient.Client` 的 fake Transport 契约测试，
  覆盖复用、ChainID admission、关闭和关闭后的请求拒绝。
- `acae29f`：Slice 1，将内部 dialer 改为返回 `Transport`；Geth HTTP/WSS client
  仍由 compatibility Adapter 创建和持有，Source 不再认识具体 client 类型。
- `7370988`：Slice 1b，将 Geth/HTTP compatibility Adapter、body capture 和
  presence 恢复移到 `internal/transport`；pool 生产代码不再依赖 ethclient、
  Geth rpc 或 HTTP RoundTripper。
- `7c10b1e`：Slice 1c，在 `rpc` domain 包定义项目拥有的 `ProtocolError`、
  `DataError`、`ErrorDataPresence`、`RateLimitError` 和 `HTTPStatusError`
  capability；pool、aggregator、write、livecanary 和 rpc policy 不再导入
  Geth RPC error interface。Geth Adapter 的错误链、HTTP status、rate-limit 和
  error.data presence 行为不变；`rpc.RPCError`、`Emission`、`Observation`、
  Decision、fault、revert 和 write rejection 语义不变。
- `69d19f8`：Slice 1c 审查修正，livecanary 的 `isRateLimit` 补充
  `rpc.RateLimitError` capability 检查（与 pool 的退避判定同构）；纯
  capability 的限流错误不再依赖 HTTP status、已知错误码或错误文本才被归类为
  `provider_rate_limited`。
- `21eedd7`：Slice 1d，锁定 go-ethereum v1.17.5 HTTP compatibility baseline，
  明确 Geth 行为与 EthGuard 规范的分层；baseline 不授权复制响应 ID 不校验、
  degenerate error 健康语义或 HTTP Close no-op 等偶然行为。
- 下一片：主维护者裁决 EthGuard HTTP contract；裁决完成前不开始自有 HTTP/WSS
  客户端实现。
- 当前没有自有 HTTP 或 WSS client，也没有修改 `RPCError`、`Emission`、
  `Observation`、Decision 或公开 ethadapter 表面。

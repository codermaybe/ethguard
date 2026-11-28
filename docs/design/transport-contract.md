# Transport L1 Contract（连接适配器外部行为规范）

> 状态：2026-11-21 两层响应出口已实施；native HTTP L1 行为起于 2026-10-29。本文件拥有 native HTTP
> 的 HTTP 外部行为；`internal/transport/contract` 是其可执行形式。历史 geth
> 差分记录仅保留为当时的 geth 对照证据，不构成 native 的运行时依赖或额外承诺。WSS 的帧与连接扩展由
> [wss-wire-contract.md](wss-wire-contract.md) 拥有，流语义由
> [subscription-contract.md](subscription-contract.md) 拥有；下方 dated evidence
> 仅记录历史观察，不扩张 L1 或兼容性承诺。

## 0. 分层

| 层 | 内容 | 契约强度 |
| --- | --- | --- |
| **L1 外部行为** | 本文 §2，所有 adapter 必须一致，suite 逐条断言 | 硬 |
| **L2 额外限制** | 资源/诊断类限制，必须文档化：非 2xx body 截断长度（native 4KiB）、诊断字段长度、连接池参数 | 不得拒绝 L1 认为语义有效的响应，不得改变 L1 错误类别 |
| **L3 诊断差异** | 错误文本、trailing data/重复 key 的具体描述、包装错误的具体形状 | 允许不同，但必须映射到同一 L1 分类 |

## 1. 适配器与运行形态

- `internal/transport` 是 Port-only 物理包：`Transport`、`Dialer`、`ErrBodyTooLarge`、
  `ErrConnectionClosed`。适配器实现不得进入该包。
- native HTTP（`internal/transport/http`）是唯一生产 HTTP adapter。`internal/endpoint` 统一解析端点身份与拨号目标；`config.Normalize` 推导 transport、补齐诊断 id 并冻结相对 IPC 路径。HTTP(S) 使用原生 HTTP，WS(S) 使用 raw WebSocket；显式 transport 必须与端点匹配。不提供 HTTP adapter 单选开关或 dialer 注入。IPC 使用 Unix socket 或 Windows named pipe；Unix 实测已通过，Windows 实现与真实 pipe CI 用例已落地，本机仅完成 Windows 交叉编译／vet。
- `BoundedSubscriberDialer` 声明是否支持有界订阅，pool 在构造期保存不可变能力快照，`SubscriptionSources` 不再按 URL scheme 筛选。实际建流仍校验 `BoundedSubscriber`；能力声明不触发拨号，也不随健康或重连改变订阅分母。无可订阅来源返回内部 `ErrNoSubscriptionSource`。
- livecanary 新运行固定记录 native provenance；CLI／workflow 不提供 adapter 选择。历史 manifest 的 `HTTPAdapter` 与解析能力保留，geth 观察不能重标为 native 或重跑。
- native 生产执行图不得链接 `ethclient`；具体 geth RPC client 仅存在于 `internal/testkit/gethoracle`／显式诊断工具等允许位置；公开业务签名使用生成模型，私有算法桥不承担 transport。

### 共享双工连接合同

`internal/transport/streamrpc` 独占双工 RPC pending/demux、通知队列与租约、绑定前缓存、建立取消及 graceful/abort；只依赖 `rpcwire`、中立 Port 与 `rpc`。WSS 和 IPC 通过 `MessageConn` 提供独立消息字节、物理写锁和立即 Close。

`WriteMessage(ctx, payload) (written bool, err error)` 中 written 表示任何字节已落线。完整成功为 `(true,nil)`；只有发送前证明零写、返回精确 `ctx.Err()` 的取消保持连接健康。部分写、短写、物理错误（含实际写被取消期限打断而报告零字节）与 `(false,nil)` 违约均 abort。物理写取消回调在释放唯一写锁前收束，不能修改下一次写的 deadline；30s 为写等待上限，较短调用 deadline 优先。

物理 Close 立即解除读写和锁等待；RPC Close 拒绝新调用、停止通知并清除 pre-bind，保留在途 unary 至结算后关闭物理连接。真实故障即使发生在 closing 期间仍可 abort，首次终止者独占失败原因。通知生产失败先排空 FIFO，显式退订／Close 丢弃未读并等待 pump；已交付租约由接收者释放恰好一次。

### IPC 边界与平台

`internal/transport/ipc` 只消费已冻结的绝对 Unix 路径或规范 pipe 拨号目标。Unix 使用标准库 `net.Dialer`；Windows 文件独占 `go-winio.DialPipeContext`，其他平台返回明确不支持错误。构造及端点归一不触发网络，后续 `cwd` 改变不改变目标。拨号前取消本地拒绝；拨号完成时发现取消则关闭刚取得的连接，建流成功后不把拨号 context 绑定为连接生命周期。

有界扫描器按顶层 JSON 对象的词法边界分帧，支持相邻对象、多行、空白分隔、分片及字符串转义；不按换行切分，也不承担 JSON-RPC 语义验证。顶层 batch／标量、容器失配、超过 32 层嵌套、截断 EOF 均终止连接；默认单消息上限为 32 MiB，超限保留 `ErrBodyTooLarge` 与 `ErrConnectionClosed`。每次交付独立切片，单消息容量不超过上限；固定 4 KiB 读取缓存及增长时的新旧分配不等于单消息长度，因此该上限不是进程 RSS 上限。消息之间的空白不累积保留，也不计入单消息预算；当前没有另设其总字节或耗时上限。因此持续空白流的读取工作量无界，但不会因累计保存空白而增长内存；这项抗恶意对端加固与单消息内存限界分别记账。

写侧复制调用方 payload 后追加换行，分配前检查加一后的长度不会溢出 int，采用共享核的精确写进度合同。实际短写或物理超时立即终止；写取消回调必须在释放唯一写锁前退出。普通调用和订阅共用同一连接及唯一 RPC 状态机。

2026-11-28 的本地验收覆盖真实 Unix socket 的乱序响应、协议错误 presence、取消、graceful Close、pre-bind 与 unary 共线，以及公开入口的单源 moving-tag、WSS+IPC 三类订阅、HTTP+IPC 单能力来源拒绝和相对路径冻结；定向 race 与 100000 次扫描器 fuzz 通过。Windows 测试二进制和 vet 交叉检查通过，真实 pipe 测试已加入 Windows CI，但本机没有 Windows 运行证据；这些结果也不构成各 execution 客户端的 IPC 互操作认证。

同日 PR #13 首轮远端 Windows build／vet 和 streamrpc／IPC 整包 race 通过；后续 adapter 测试失败使显式 named pipe 步骤跳过，Windows job 整体仍失败。该结果与交叉编译及本地容量修复分开记账，详见[远端检查记录](access-plane-ipc-adapter.md#pr-13-首轮远端检查与容量修复2026-11-28)。

## 2. L1 条款（suite 逐条对应）

1. **调用语义**：一次调用恰是一个 JSON-RPC 请求；服务端 5xx 不会被自动重试或
   重放（suite: `no automatic retry`）。请求携带 method、params、非空 id。
2. **响应交付**：`Transport.Call(ctx, method, args...)`返回`rpc.Response{Exchange, Answer}`，
   直接拥有结果字节；旧输出指针出口已删除。`Response.Decode`是消费者可选解码，支持
   nil、`*json.RawMessage`及JSON指针；无效目标是本地错误，不改变回答事实。
3. **协议错误能力**：JSON-RPC error 对象 → 调用方错误满足 `rpc.ProtocolError`
   （code/message）与 `rpc.DataError`；data 缺省时 `rpc.ErrorDataPresence` 报
   absent（suite: `protocol error capabilities`）。
4. **data 存在性保真**：data 为字符串/对象/null 时，presence 报 present 且
   原始字节逐字保留（`null` 也是 present）——空 data 与 null data 不可混同
   （suite: `protocol error data presence`）。
5. **退化 error 成员**：`error: {}` / `null` / 字符串 → malformed-response
   哨兵，**绝不**冒充 code-0 协议错误（那会被下游计为可用答案）
   （suite: `degenerate error members`）。
6. **HTTP 状态分类**：非2xx在Exchange保留状态码，不将正文解释成RPC回答；最终错误
   满足`rpc.HTTPStatusError`，429额外满足`rpc.RateLimitError`。短正文保留读取错误能力，
   正文超过诊断上限同时保留`ErrBodyTooLarge`；此时进度为Responded。
7. **坏响应类别**：空 body → `ErrEmptyResponse`；非 JSON / JSON 数组 body /
   完整 JSON 后跟 trailing garbage → `ErrMalformedResponse`；这些都不得以
   ProtocolError 形状出现（suite: `empty and malformed bodies`、`trailing data`）。
8. **重复 key**：HTTP envelope 同名字段按该层现有 last-wins 规则解码；这不授权业务对象重复 key。来源 result 仍经 aggregator／对象层 JSON 安全检查拒绝重复声明。重复的具体诊断属 L3（suite: `duplicate keys last win`）。
9. **上下文优先级与事实保留**：取消/超时优先决定`Response.Err()`，调用前取消为
   NotStarted；已收响应头后取消仍保留Responded和HTTP状态，不能伪称无响应。若已完整
   取得回答，Completed及Answer继续存在，Decode遵循取消错误而不交付值。
10. **Body 上限**：响应 body 超过 `limits.MaxBodyBytes`（或 adapter 配置的
    等价上限）返回中立哨兵 `transport.ErrBodyTooLarge`；截断前缀不得进入
    解码器（suite: `body limit`）。
11. **生命周期**：`Close` 幂等；Close 后新调用返回中立哨兵
    `transport.ErrConnectionClosed`，不接触服务端；已在途的调用由自己的
    context 决定，Close 不打断（suite: `close lifecycle`、
    `in-flight call survives close`）。
12. **并发**：一个 Transport 可被并发调用，各自交付各自的结果（suite:
    `concurrent calls`）。
13. **ChainID 探测**：只发一次 `eth_chainId`；错误走同一套能力（suite:
    `chain id probe`）。
14. **无方法知识**：Transport/Dialer 不参与 anchor、quorum、admission——这是
    结构性条款，由依赖 interlock 的 import 矩阵保证，不进本 suite。

Exchange进度是本地可观测事实：Started不承诺请求已到达或远端已执行，Responded不承诺正文完整；Completed不承诺HTTP成功或方法有效。Answer区分Unassessed、Missing、Invalid、Result和ProtocolError，合法null属于Result。默认零Response不能报告成功；方法检查继续由上层owner负责。

## 3. 已记录的实现差异（L2/L3）

| 历史差异 | native | 历史 geth adapter | 层 |
| --- | --- | --- | --- |
| 错误文本 | project sentinels（`rpcwire`） | 同一哨兵 + geth 原始错误文本（wrap 链） | L3 |
| 非 2xx body 保留 | 4KiB 截断前缀 | 完整捕获（受 32MiB 上限） | L2 |
| 尾随垃圾（HTTP） | 拒绝（单值解码） | 拒绝（捕获路径以 `rpcwire.ValidateBody` 复检原始 body） | 已对齐 |
| 尾随垃圾（geth WSS） | native HTTP 不适用；自有 WSS 见 wire 契约 | geth codec 逐帧解码，无法复检 —— 已记录偏差 | 独立传输边界，不是 HTTP suite 的兼容性证明 |
| 响应 id 匹配 | 校验 | 不校验（单调用 HTTP 按位置匹配；suite 不钉此差异，契约 §2.1 的请求 id 存在性仍成立） | L2 |
| ws/wss | native HTTP dial 时拒绝；自有 `internal/transport/wss` 独立支持 | 经 geth 支持 | 构造期差异；自有 WSS 范围见 wire 契约 |

当时两 HTTP adapter 共享 Close 后调用的中立哨兵、退化 error 的 malformed 分类、
空/坏 body 的 project 哨兵、错误版本 envelope 拒绝和 HTTP 尾随垃圾拒绝。当前生产
规范由 native adapter 实现；历史 geth L1 对照使用过独立 adapter。差分 oracle 位于
`internal/testkit/gethoracle`，只供测试和显式诊断，历史报告仍注明原 adapter 身份。

## 4. 与既有测试的关系

- `internal/transport/http/differential_test.go` 保留为**诊断**：它记录 native 与
  内部 oracle 的
  逐用例的行为对照，不再是 native 的规范性 gate。
- `internal/transport/http/http_test.go`、`rawbody_test.go` 继续覆盖
  adapter 内部行为；suite 只断言跨 adapter 必须一致的外部行为。

## 5. 2026-10-25 native HTTP 本地 provider 诊断

本节合并原 `live-canary/runs/2026-10-25-native-smoke-local.md` 的有用证据。
这是作者本机的两次只读 smoke，产物未发布，**不是 external live observation、
attested evidence 或当前默认适配器的兼容性认证**。当时 native 尚非默认；历史
`internal/transport.StandardDialer{}` 对照构造名称不是当前 API。

- 工具：`cmd/nativesmoke`，当时工作树的 Slice 2 + review fix + adapter selection
  实现；native 显式使用 `internal/transport/http.NewDialer()`，geth 并行对照。
- 每 provider/adapter 复用一条 transport，只调用 `eth_chainId`、`eth_blockNumber`、
  `eth_getBlockByNumber("latest", false)` 与 mainnet USDT `transfer` 到 dead recipient
  的确定性 `eth_call` revert；不提交交易，不使用凭据，不测试订阅。
- 第一轮使用默认 provider 集；第二轮命令追加/指定
  `-providers "https://eth.meowrpc.com,https://rpc.mevblocker.io,https://rpc.ankr.com/eth"`。
  两轮输出分别为 `report1.json`、`report2.json`，均保留在仓库外。

| Provider | 当时观察（不是长期兼容性结论） |
| --- | --- |
| ethereum-rpc.publicnode.com、eth.drpc.org、eth-mainnet.public.blastapi.io | native envelope 合法，chain ID `0x1`；revert code 3、data missing |
| 1rpc.io/eth | native envelope 合法，chain ID `0x1`；revert code 3、`data:"0x"` 保真 |
| cloudflare-eth.com | 第一轮两 adapter 同为 HTTP 429；第二轮成功、chain ID `0x1` |
| rpc.mevblocker.io | 成功、chain ID `0x1` |
| eth.meowrpc.com | chain ID 合法；瞬时 429；第二轮 geth 限流而 native 成功，不能据此归因 adapter |
| eth.llamarpc.com | 所有调用两 adapter 同为 Cloudflare HTTP 525 |
| rpc.ankr.com/eth（无 key） | HTTP 200 error envelope 的 `id:null`：native `ErrIDMismatch`，geth `-32000` protocol error |

六个不同 provider 在健康响应上通过 native 的 ID/version/result-error/error-shape
检查；样本为每轮 6–9 个尝试端点，其中 2–3 个受可用性限制。missing 与 `0x`
在真实 wire 出现并保真；这不证明显式 `null`，也不是四态证据。

Ankr 的 curl 原文为 `{"jsonrpc":"2.0","error":{"code":-32000,...},"id":null}`，
报告记为 `divergent_failure`。两 adapter 都无法提供可用业务答案，但错误分类与
health accounting 不同；当时提出兼容模式裁定，**本次整理不放松 ID 校验**，
也不把未测试的带 key 成功路径当作已兼容。

HTTP 429/525 的分类在两实现相同，保留的 body 文本携带 pool 等价限流信号。
另一次 1rpc 的 HTTP-200 result 为 `{"code":503,"message":"server unavailable"}`，
两实现均当作 result 交付；transport 不替代下游内容裁决。连接复用与 RTT 仅作
当时的定性正常观察，不形成性能承诺。

| 仓库外产物 | SHA-256（不是发布 attestation） |
| --- | --- |
| report1.json | `84291e8b5da1e08fda2400ca66bc658d4d51d1cfbff7ff56f9242fb8f3aa34f6` |
| report2.json | `e7463db73fa04b73cc9374501cc5917594f425cc3092fec997fa7ae987a74078` |

外部 native canary 与其 artifact recheck 不由这两次本机 smoke 证明；
`inconclusive` 始终不是成功。

## 6. 待后续实现阶段评估

历史无凭证 Ankr 的 id:null 分类差异仍是兼容政策问题，本轮不放松 ID。HTTP／WSS chain-ID 容量与语法需按信任层复核；不能因函数相似强制统一。协议层完整性、来源形态准入和typed错误分层验证，不把HTTP envelope行为外推到所有业务JSON。

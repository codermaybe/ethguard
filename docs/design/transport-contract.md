# Transport L1 Contract（连接适配器外部行为规范）

> 状态：已冻结（T0，2026-10-29）。本文件是 native HTTP 适配器与 geth 兼容适配器
> 共同的外部行为契约；`internal/transport/contract` 套件是它的可执行形式。
> 本文取代 `http-transport-differential.md` 的规范性地位：那份文档降级为兼容性
> 诊断说明。基线是 **EthGuard 自有 contract**，geth 只是兼容实现。

## 0. 分层

| 层 | 内容 | 契约强度 |
| --- | --- | --- |
| **L1 外部行为** | 本文 §2，所有 adapter 必须一致，suite 逐条断言 | 硬 |
| **L2 额外限制** | 资源/诊断类限制，必须文档化：非 2xx body 截断长度（native 4KiB）、诊断字段长度、连接池参数 | 不得拒绝 L1 认为语义有效的响应，不得改变 L1 错误类别 |
| **L3 诊断差异** | 错误文本、trailing data/重复 key 的具体描述、包装错误的具体形状 | 允许不同，但必须映射到同一 L1 分类 |

## 1. 适配器与运行形态

- `internal/transport` 是 Port-only 物理包：`Transport`、`Dialer`、`ErrBodyTooLarge`、
  `ErrConnectionClosed`。适配器实现不得进入该包（L0 拆包后生效）。
- native HTTP（`internal/transport/http`）是战略默认；geth 兼容适配器经
  `gethcompat` 显式构造（L1 阶段落地）。
- 默认构建不得链接 `ethclient`；`gethrpc` 只允许出现在
  `internal/transport/geth`、`ethadapter`、`cmd/*`。

## 2. L1 条款（suite 逐条对应）

1. **调用语义**：一次调用恰是一个 JSON-RPC 请求；服务端 5xx 不会被自动重试或
   重放（suite: `no automatic retry`）。请求携带 method、params、非空 id。
2. **result 目标**：`result` 只接受 `nil`（丢弃）、`*json.RawMessage`（原样字节）、
   其他 JSON 指针；非指针目标报调用方错误（suite: `result target contract`）。
3. **协议错误能力**：JSON-RPC error 对象 → 调用方错误满足 `rpc.ProtocolError`
   （code/message）与 `rpc.DataError`；data 缺省时 `rpc.ErrorDataPresence` 报
   absent（suite: `protocol error capabilities`）。
4. **data 存在性保真**：data 为字符串/对象/null 时，presence 报 present 且
   原始字节逐字保留（`null` 也是 present）——空 data 与 null data 不可混同
   （suite: `protocol error data presence`）。
5. **退化 error 成员**：`error: {}` / `null` / 字符串 → malformed-response
   哨兵，**绝不**冒充 code-0 协议错误（那会被下游计为可用答案）
   （suite: `degenerate error members`）。
6. **HTTP 状态分类**：非 2xx → `rpc.HTTPStatusError`（携带状态码）；429 额外
   满足 `rpc.RateLimitError`；非 429 不得声称 rate limit（suite:
   `http status classification`）。
7. **坏响应类别**：空 body → `ErrEmptyResponse`；非 JSON / JSON 数组 body /
   完整 JSON 后跟 trailing garbage → `ErrMalformedResponse`；这些都不得以
   ProtocolError 形状出现（suite: `empty and malformed bodies`、`trailing data`）。
8. **重复 key**：同名字段 last-wins，调用成功并交付最后一次拼写。两实现共享
   该类别；重复的具体诊断属 L3（suite: `duplicate keys last win`）。
9. **上下文优先级**：取消/超时支配一切 body 结果——调用前取消返回取消错；
   挂死服务端上 deadline 返回 `DeadlineExceeded`；读非 2xx body 途中取消，
   丢弃状态结果返回 context 错误（suite: `context precedence`）。
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
    结构性条款，由依赖 interlock（L2 阶段的 import 矩阵）保证，不进本 suite。

## 3. 已记录的实现差异（G0 对齐后，L2/L3）

| 差异 | native | geth 兼容 | 层 |
| --- | --- | --- | --- |
| 错误文本 | project sentinels（`rpcwire`） | 同一哨兵 + geth 原始错误文本（wrap 链） | L3 |
| 非 2xx body 保留 | 4KiB 截断前缀 | 完整捕获（受 32MiB 上限） | L2 |
| 尾随垃圾（HTTP） | 拒绝（单值解码） | 拒绝（捕获路径以 `rpcwire.ValidateBody` 复检原始 body） | 已对齐 |
| 尾随垃圾（WSS） | 不适用（native 无 WSS） | geth codec 逐帧解码，无法复检 —— 已记录偏差 | L2 |
| 响应 id 匹配 | 校验 | 不校验（单调用 HTTP 按位置匹配；suite 不钉此差异，契约 §2.1 的 id 存在性仍成立） | L2 |
| ws/wss | dial 时拒绝 | 经 geth 支持（直到 native WSS 设计落地） | 构造期差异，见提案 §9.1 |

G0 已修掉的 L1 差异（原基线 quirk，现为契约行为）：Close 后调用（中立哨兵）、
退化 error 成员（malformed，非 code-0 协议错误）、空/坏 body（project 哨兵）、
错误版本 envelope（malformed）、HTTP 尾随垃圾（拒绝）。两个 adapter 现在跑同一
contract suite（`internal/transport/geth/contract_test.go`）。

## 4. 与既有测试的关系

- `internal/transport/http/differential_test.go` 保留为**诊断**：它记录两实现
  逐用例的行为对照，不再是 native 的规范性 gate。
- `internal/transport/http/http_test.go`、`rawbody_test.go` 继续覆盖
  adapter 内部行为；suite 只断言跨 adapter 必须一致的外部行为。

# HTTP Transport 行为差分审计（Slice 1d）

状态：审计完成，未实现自有 HTTP client。本文记录当前 Geth HTTP compatibility
Adapter 的可观察行为，作为版本化兼容证据和差分输入；它不是自有 HTTP Adapter
必须永久复制的规范。

- 代码基线：ethguard `3781b93`（Slice 1c 之后，工作树干净）。
- 依赖基线：go-ethereum `v1.17.5`（`go.mod` 固定）。
- 审计对象：`internal/transport` 的 `StandardDialer` HTTP 分支，即
  `dialHTTPPreservingErrorData`（geth `rpc.DialOptions` + `capturingTransport`）
  加 `gethTransport` 包装。生产路径未做任何修改。
- 方法：读 go-ethereum `rpc` 包源码 + 在 `internal/transport` 增加 baseline
  测试，用 in-process HTTP server 锁定每一条可观察行为。所有测试均为
  loopback，无外网 I/O。

结论摘要：

1. 未触发停止条件：没有任何已审计行为要求修改 `Transport`、`Emission`、
   `Observation`、`RPCError` 或 pool 契约；`CallContext/ChainID/Close` 三方法
   足以承载全部行为。
2. 上层只消费项目自有 capability（`rpc.ProtocolError/DataError/
   ErrorDataPresence/HTTPStatusError/RateLimitError`）。自有 HTTP Adapter
   **不需要**复制 go-ethereum 的具体错误类型，只需要复刻行为与 capability。
3. 共 6 项进入"需主维护者裁决"（见 §4），其中影响最大的是：响应 ID/版本
   不校验（2.8）与 degenerate error 成员的健康语义（3.10）。

### 基线与规范的分离

本矩阵绑定 go-ethereum `v1.17.5`。截至 2026-10-25，官方最新发布已是
`v1.17.10`；其 `rpc/http.go` 和 `rpc/json.go` 已调整 HTTP server 的 frame
读取、body limit 和 JSON frame 解析路径，而公开 client 的 `CallContext` 形状仍
相近。参见 [v1.17.10 release](https://github.com/ethereum/go-ethereum/releases/tag/v1.17.10)、
[v1.17.10 rpc/http.go](https://github.com/ethereum/go-ethereum/blob/v1.17.10/rpc/http.go)
和 [v1.17.10 rpc/json.go](https://github.com/ethereum/go-ethereum/blob/v1.17.10/rpc/json.go)。

因此，v1.17.5 baseline 只回答“当前兼容 Adapter 在这一依赖版本下做什么”；升级
Geth 时应重跑 baseline 并生成版本差异。矩阵中的 B 类行为（响应 ID/版本不校验、
degenerate error、具体 EOF/JSON 错误形态、HTTP Close no-op）不自动成为自有
Adapter 的长期契约。A 类中的 pool、health、capability 和 `error.data` presence
语义才是 EthGuard 需要明确维护的稳定接口。

---

## 1. 证据约定

| 简写 | 含义 |
| --- | --- |
| `geth:rpc/http.go doRequest` | go-ethereum v1.17.5 `rpc` 包源码，按函数引用（行号随版本漂移，函数名不漂移） |
| `baseline:TestX` | `internal/transport/geth_http_baseline_test.go`（本次新增） |
| `transport:TestX` | `internal/transport/transport_test.go`（既有） |
| `capture:TestX` | `internal/transport/rawbody_test.go`（既有） |
| `pool:TestX` | `pool` 包既有测试 |

类别（对全部矩阵行适用）：

- **A 必须保持的现有语义**：pool/上层已依赖的契约。自有 Adapter 必须表现出
  相同的可观察行为，否则 health、可用答案判定或证据回退。
- **B Geth 私有实现，但上层可观察**：当前行为被 baseline 测试锁定；上层目前
  依赖其稳定性。改动不是不可以，但必须先过裁决并同步更新 baseline。
- **C pool 不承诺的行为**：Geth/网络栈的实现细节，上层不得依赖；差分时出现
  偏差不算破坏。
- **D 需要主维护者裁决的行为**：兼容性要求与"最小实现"冲突，或收紧/放宽都会
  改变可观察行为。

A 类是当前 compatibility Adapter 的替换安全线；自有 Adapter 只有在 §4 明确裁决
后，才可以在 D 类行为上采用更严格的 EthGuard 规范。baseline 本身不自动成为
长期标准。

## 2. 差分矩阵

"必须兼容"列：`yes` = 自有 Adapter 必须保持；`no` = 可不保持；`需裁决` = 见 §4。

### 2.1 请求 envelope

| 场景 | Geth v1.17.5 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| method/verb/headers | POST；固定 `accept`/`content-type: application/json`；`ContentLength` 与 `GetBody` 恒设置；method 用 `jsonw.AppendQuotedString` 编码 | A | yes | Adapter | `geth:rpc/http.go newClientTransportHTTP/doRequest`；`baseline:TestHTTPBaselineRequestEnvelope` |
| request ID 规则 | 每 client 一个原子计数器，从 1 开始单调递增，十进制 JSON number；`CallContext` 与 `ChainID` admission 共用同一计数器 | B | 需裁决（建议原样复刻，成本为零） | Adapter | `geth:rpc/client.go nextID/newMessage`；`baseline:TestHTTPBaselineRequestIDIsMonotonicCounter` |
| params == nil | wire 上完全省略 `params` 成员，绝不发送 `"params":null` | A | yes | Adapter | `geth:rpc/client.go newMessage`（"prevent sending params:null"）；`baseline:TestHTTPBaselineRequestEnvelope/no-args-omits-params` |
| 空 params（显式 `[]any{}`） | 发送 `"params":[]`（与 nil 可区分） | A | yes | Adapter | `baseline:TestHTTPBaselineRequestEnvelope/empty-non-nil-slice-sends-empty-array` |
| 普通 params | JSON 数组、保序；参数 marshal 失败时不发包、原样返回错误 | A | yes | Adapter | `baseline:TestHTTPBaselineRequestEnvelope/args-become-array` |
| 并发请求 | 每个 `CallContext` 独立 POST；wire 层从不 batch；ID 原子唯一；单 client 并发安全 | A | yes | Adapter | `baseline:TestHTTPBaselineConcurrentCallsAreIndependentPOSTs` |
| 自定义 header/认证 | `SetHeader`、`httpHeaders`、`HTTPAuth` 存在但 pool 当前一律不用 | C | no | Adapter | `geth:rpc/http.go doRequest`；`pool` 无调用点 |

### 2.2 正常响应

| 场景 | Geth v1.17.5 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| 普通 result | 解码进 result 指针；`*json.RawMessage` 保留原始字节 | A | yes | Adapter | `geth:rpc/client.go CallContext`；`capture:TestCaptureHTTPNormalStates/success-result` |
| result: null | 成功返回；raw value 是字节 `null`（不是 ErrNoResult，也不得变成失败） | A | yes | Adapter | `baseline:TestHTTPBaselineNormalResponseForms/result-null-is-success` |
| 缺少 result 成员 | `gethrpc.ErrNoResult` 哨兵；非 ProtocolError → pool 记失败、非可用答案 | A | yes（"返回错误且非 ProtocolError"必须保持；哨兵具体类型不要求复制） | Adapter + pool | `baseline:TestHTTPBaselineNormalResponseForms/missing-result-is-ErrNoResult`；`geth:rpc/client.go CallContext` |
| 空 body（200） | `json.Decoder.Decode` 返回 `io.EOF`，原样上抛 | B | yes（必须失败且非可用答案）；具体错误形态需裁决 | Adapter | `baseline:TestHTTPBaselineEmptyBodyAndStatus204AreEOF/200-empty-body` |
| malformed JSON | encoding/json 原始错误：值内垃圾 → `*json.SyntaxError`；类型不符 → `*json.UnmarshalTypeError`；EOF 截断 → `io.ErrUnexpectedEOF`；一律不再分类 | B | yes（失败、非 ProtocolError、非 presence 包装）；具体错误形态需裁决 | Adapter | `baseline:TestHTTPBaselineMalformedBodySurfacesJSONError` |
| 错误 JSON-RPC ID | **完全不校验**：单调用路径按"一调用一响应体"取首个消息，`id`/`jsonrpc` 版本与请求不比对即接受 | B | 需裁决（不建议顺手收紧，见 §4.1） | Adapter | `baseline:TestHTTPBaselineResponseIDAndVersionNotValidated`；`geth:rpc/http.go sendHTTP` + `rpc/client.go CallContext` |
| error 与 result 同时出现 | error 优先（decode 先查 Error） | B | yes（建议保持，删除有证据回退风险） | Adapter | `baseline:TestHTTPBaselineErrorMemberDegenerateForms/error-and-result-both-present` |

### 2.3 JSON-RPC error

| 场景 | Geth v1.17.5 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| error.code | `jsonError.Code`；缺失/非数字 → 0 | B | yes（分类稳定性依赖） | Adapter | `geth:rpc/json.go decodeError`；`baseline:TestHTTPBaselineErrorMemberDegenerateForms` |
| error.message | `Error()` 返回 message；空 message → `"json-rpc error %d"` | B | yes | Adapter | 同上 |
| error.data 缺失 | `ErrorDataJSON()` presence=false；decoded any 为 nil；不得声称 presence | A | yes | Adapter + aggregator | `baseline:TestHTTPBaselineErrorDataVariants/absent`；`capture:TestPreserveErrorDataDistinguishesMissingAndNull`；`aggregator/aggregator.go observationRPCError` |
| error.data: null | presence=true，raw 字节 `null`；decoded any 仍为 nil（塌缩正是 presence 存在的理由） | A | yes | Adapter | `baseline:TestHTTPBaselineErrorDataVariants/null`；`capture:TestPreserveErrorDataDistinguishesMissingAndNull` |
| error.data 具体 JSON | raw 原样字节保留（对象/数组/数字/字符串均可）；decoded any 走 encoding/json 默认（数字→float64） | A（raw）/ B（decoded 形态） | yes | Adapter | `baseline:TestHTTPBaselineErrorDataVariants` |
| ProtocolError capability | 一切成功解码的 JSON-RPC error 都满足（含 code 0），`ErrorCode()` 透传 | A | yes | Adapter + pool | `rpc/protocol.go`；`baseline:TestHTTPBaselineErrorDataVariants` |
| DataError | geth `jsonError.ErrorData()` 可经 `preservedDataError.Unwrap` 被 `errors.As` 找到（`ethclient.RevertErrorData` 与 `rpc.DataError` 继续工作） | A | yes | Adapter | `baseline:TestHTTPBaselineErrorDataVariants`；`internal/transport/rawbody.go preservedDataError` |
| ErrorDataPresence | 仅 HTTP capture Adapter 提供；WSS/无 envelope Adapter 不可提供，presence 保持 unknown | A | yes | Adapter + aggregator | `rpc/protocol.go`；`docs/design/wss-error-data-presence.md` |
| errors.As 错误链 | `preservedDataError → geth jsonError`、`httpStatusError → gethrpc.HTTPError`；capability 在链上任意层可被 `errors.As` 命中 | A | yes | Adapter | `baseline:TestHTTPBaselineErrorDataVariants`；`transport:TestAdaptErrorPreservesHTTPStatusCapability` |
| degenerate error 成员（`{}`、`null`、`"boom"`） | code 0 + message `"json-rpc error 0"`；仍是 ProtocolError → pool 视为可用答案，且（code 0 不在 server-error/parse/invalid-request 集合）**计为健康成功** | B | yes（现状冻结；改动即 pool 行为漂移，见 §4.2） | Adapter + pool | `baseline:TestHTTPBaselineErrorMemberDegenerateForms`；`pool/monitor.go isHealthyRPCResponse` |

### 2.4 HTTP 层

| 场景 | Geth v1.17.5 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| 200 | 进入 2xx 解码路径 | A | yes | Adapter | `baseline` 全部正常态测试 |
| 204 | 通过 2xx 状态 gate（`<200 || >=300` 才算失败），body 空 → 解码 `io.EOF` | B | yes（必须失败）；`io.EOF` 具体形态需裁决 | Adapter | `baseline:TestHTTPBaselineEmptyBodyAndStatus204AreEOF/204-no-content` |
| 400/401/403/404/500 | `gethrpc.HTTPError{Status, StatusCode, Body}`；Body 原样进错误链，`Error()` 文本含 body | A | yes | Adapter | `baseline:TestHTTPBaselineNon2xxBecomesHTTPErrorWithBody`；`geth:rpc/http.go doRequest` |
| HTTP body 是否进入错误链 | 非 2xx：是（Body 字段 + 消息文本；pool 的文本限流规则据此匹配）；2xx JSON-RPC error：仅用于 Adapter 内部 presence 恢复，不进消息；2xx 成功：否 | A | yes | Adapter | 同上 + `baseline:TestHTTPBaseline429CarriesRateLimitCapability` |
| 非 2xx 携带 JSON-RPC error body（含 data） | 不解析为 ProtocolError、不声称 presence；pool 记传输失败而非可用答案 | A | yes | Adapter + pool | `baseline:TestHTTPBaselineNon2xxBecomesHTTPErrorWithBody`（ProtocolError 断言为否） |
| HTTPStatusError capability | 仅非 2xx 由 `adaptError` 包装产生；2xx JSON-RPC error 不携带；`HTTPStatusCode()` = 原始状态码 | A | yes | Adapter + livecanary | `transport:TestAdaptErrorPreservesHTTPStatusCapability`；`baseline:TestHTTPBaselineNon2xxBecomesHTTPErrorWithBody`；`internal/livecanary/evidence.go` |
| 429 | `httpStatusError.IsRateLimit()=true`；pool 计 `RateLimited` 且调度 pace ×2；livecanary 归类 `provider_rate_limited` | A | yes | Adapter + pool + livecanary | `baseline:TestHTTPBaseline429CarriesRateLimitCapability`；`pool:TestMonitorCountsRateLimitsDistinctly`；`pool/monitor.go adaptFailureLocked`；`69d19f8` |
| 非 429 限流信号 | JSON-RPC code `-32005/-32016/429` 或错误文本含 `rate limit`/`too many requests`/`throttl` 同样归类限流（依赖错误消息文本） | A | yes | pool | `pool/monitor.go isRateLimitError` |

### 2.5 body 与 context（capture 路径；这是 ethguard 自有机制，非 Geth 行为）

| 场景 | 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| body 上限 | 默认 32 MiB 字节计数上限，仅为 presence 恢复读取；Geth 客户端侧无此限制（其服务端默认 5 MB） | A（自有语义） | yes | Adapter | `internal/transport/rawbody.go defaultCapturedBodyBytes` |
| 正好达到上限 | 成功；body 原样交给 Geth 解码 | A | yes | Adapter | `baseline:TestHTTPBaselineBodyExactLimitSucceeds/exact-limit-is-success` |
| 超过上限 | `ErrBodyTooLarge` 包装；截断前缀永不进入解码或 presence 声称 | A | yes | Adapter | `baseline:TestHTTPBaselineBodyExactLimitSucceeds/one-byte-over`；`capture:TestCaptureHTTPBodyTooLargeIsClassifiedErr` |
| body 中途读取失败 | 原样返回读错误；box 保持空；不误分类为 ErrBodyTooLarge/context/protocol | A | yes | Adapter | `capture:TestCaptureHTTPReadFailureSurfacesReadError` |
| context cancel | 优先级最高（层 1）：即便同时超限/断连也返回 `context.Canceled` | A | yes | Adapter | `capture:TestCaptureHTTPContextCancelBeatsTruncation` |
| deadline | 同层 1：`context.DeadlineExceeded` 支配 truncation/read/decode | A | yes | Adapter | `baseline:TestHTTPBaselineDeadlineExceededDominates` |
| cancel 与 body 超限同时 | cancel 赢（层 1 > 层 2 > 层 3，严格序） | A | yes | Adapter | `capture:TestCaptureHTTPContextCancelBeatsTruncation`；`rawbody.go RoundTrip` 注释 |
| 时间边界 | Adapter 不注入自己的 deadline；唯一时间界限是调用方 context | A | yes | Adapter | `rawbody.go dialHTTPPreservingErrorData` 注释 |
| 超限后恢复 | 一次超限失败不影响同 client 后续调用 | A | yes | Adapter | `capture:TestCaptureHTTPRecoveryAfterOversizedBody` |
| 单槽不变量 | 一个 call context 至多一个捕获体；第二次写入 `errRawBodySlotTaken` 拒绝 | A | yes（自有实现可以换机制，但归属错误必须 fail-loud） | Adapter | `capture:TestRawBodyBoxSingleSlotRejectsSecondSet`；`capture:TestCaptureTransportSecondCaptureFailsRoundTrip` |

### 2.6 生命周期

| 场景 | Geth v1.17.5 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| 连接复用 | dial 时不联网（HTTP 无连接建立）；每调用独立 POST；TCP keep-alive 复用由 net/http Transport 池提供（3 次连续调用共享 1 条连接） | A | yes | Adapter | `baseline:TestHTTPBaselineConnectionReuseAcrossCalls`；`geth:rpc/http.go newClientTransportHTTP` |
| 多个并发 CallContext | 单 client 并发安全；每调用独立单槽 box，互不归属 | A | yes | Adapter | `baseline:TestHTTPBaselineConcurrentCallsAreIndependentPOSTs` |
| Source close | pool 置 closed、调用 `Transport.Close()`、状态 Down；之后 `clientForCall`/`acquireRequest` 返回 `ErrClosed` | A | yes | pool | `pool/source.go close/clientForCall`；`pool:TestTransportSeamAcceptsIndependentAdapterAndReusesIt` |
| Transport close（HTTP） | geth `rpc.Client.Close` 对 HTTP 是 no-op（`isHTTP` 直接 return）；连接池不清空 | B | 需裁决（建议保持 no-op，见 §4.3） | Adapter | `baseline:TestHTTPBaselineHTTPCloseIsNoOp`；`geth:rpc/client.go Close` |
| close 后 pending request（Adapter 层） | 不被 abort，正常完成 | B | 需裁决（若自有实现主动 abort，本行与上行语义改变） | Adapter | `baseline:TestHTTPBaselineHTTPCloseIsNoOp` |
| close 后 pending request（pool 层） | `collectOne` 完成后发现 `isClosed` → 结果丢弃、返回 `ErrClosed`；不计健康失败 | A | yes | pool | `pool/collect.go collectOne/shouldRecordFailure` |
| close 后再次调用（Adapter 层） | 仍成功（close 未破坏任何状态） | B | 需裁决（pool 层无论如何都会以 `ErrClosed` 拒绝） | Adapter | `baseline:TestHTTPBaselineHTTPCloseIsNoOp` |
| ErrClientQuit | HTTP 路径永不出现（dispatch 未启动）→ `shouldDiscardClient` 永不触发，Source 不换 client | B | yes（永不触发即兼容） | Adapter + pool | `geth:rpc/client.go`；`pool/collect.go shouldDiscardClient` |

### 2.7 pool 可观察结果

| 场景 | 当前行为 | 类 | 必须兼容 | 归属 | 现有证据 |
| --- | --- | --- | --- | --- | --- |
| Emission.Val | 仅当 `Err==nil`；原始 result 字节（`result:null` → `"null"`，非 nil → 计可用答案） | A | yes | pool | `pool/collect.go Emission`；`pool:TestCollectTreatsJSONRPCErrorAsAHealthyResponse` |
| Emission.Err | Adapter 上抛的错误原样携带（含 capability）；gate 错误为 `ErrSourceGated`/`ErrChainIDMismatch` | A | yes | pool | `pool/collect.go dispatchOne` |
| Emission.Attempted | 取得调度槽并真正发起才为 true；gate/超时未尝试的成员 false 且无健康记账 | A | yes | pool | `pool/collect.go dispatchOne`（decision 0027） |
| Emission.RTT | 覆盖 dial（首次）+ ChainID admission + 调用本身；未尝试为 0 | B | yes | pool | `pool/collect.go dispatchOne/collectOne` |
| health success | `err==nil`，或"健康 JSON-RPC error"：code 非服务端错误集且非 -32700/-32600（revert -32000 含 "execution reverted"/"revert" 属于此类）→ 记成功、SourceReady | A | yes | pool | `pool/monitor.go isHealthyRPCResponse`；`pool:TestCollectTreatsJSONRPCErrorAsAHealthyResponse` |
| health failure | 其余一切（HTTP 状态错误、EOF、malformed、ErrNoResult、-32700/-32600、服务端错误 code）→ Failures++、consecutive++、SourceDown；连续 3 次开冷却闸 | A | yes | pool | `pool:TestCollectRecordsJSONRPCProtocolErrorAsFailure`；`pool:TestCollectTemporarilyExcludesRepeatedlyFailingSource` |
| rate-limit 退避 | `RateLimited++` 且 pace ×2（普通失败 ×3/2）；判定链 = RateLimitError capability → code {-32005,-32016,429} → 文本匹配 | A | yes | pool | `pool/monitor.go isRateLimitError/adaptFailureLocked`；`pool:TestMonitorCountsRateLimitsDistinctly`；commit `69d19f8` |
| protocol error 是否计为 usable answer | 是：任何满足 ProtocolError 的错误（含 degenerate code 0）终止 Forward、满足 Collect、保留 voting 基 | A | yes | pool + aggregator | `pool/collect.go isJSONRPCResponseError`；`pool:TestBroadcastProtocolErrorIsUsableAnswer` |
| protocol server errors 单列 | `-32603/-32002/-32003` 与非 revert 的 `-32000` → `ProtocolServerErrors++` | A | yes | pool | `pool/monitor.go isProtocolServerError` |
| caller cancel/deadline | 不记源健康（`parent.Err()` 检查） | A | yes | pool | `pool:TestCollectDoesNotBlameSourceForCallerDeadline`；`pool:TestCollectDoesNotBlameSourceForCallerCancellation` |

## 3. 四类行为的清单

- **A（必须保持）**：envelope 形态与 params 规则（2.1）；`result:null` 成功、
  缺 result 失败（2.2）；error.data 三态 presence（2.3）；全部 capability；
  非 2xx → HTTPError + body 入链 + 429 rate-limit（2.4）；body limit、读取
  优先级层序、context 支配（2.5，自有语义）；连接复用与并发安全（2.6）；
  pool 全部 health/gate/退避/可用答案语义（2.7）。
- **B（Geth 私有但可观察）**：request ID 计数器规则；响应 ID/版本不校验；
  degenerate error 成员的 code 0/"json-rpc error 0"；io.EOF/malformed 的具体
  错误形态；HTTP Close no-op 及其不中止在途请求；ErrClientQuit 永不出现；
  RTT 包含 dial 与 ChainID admission。
- **C（pool 不承诺）**：自定义 header/auth 能力（当前未用）；net/http 的
  User-Agent、Accept-Encoding、连接池参数、空闲超时等传输栈细节；HTTP/1.1
  与 HTTP/2 的选择。
- **D（需裁决）**：见 §4。

## 4. 需要主维护者裁决的问题

本节裁决的是自有 Adapter 的规范契约，不是要求继续复制 Geth 的偶然行为。

1. **响应 ID/版本校验（2.2）**。Geth HTTP 单调用路径完全不校验响应 id 与
   jsonrpc 版本，按位置接受。建议自有 Adapter 默认严格校验 `jsonrpc == "2.0"`
   且响应 ID 必须匹配请求 ID；若 provider evidence 证明存在合法代理改写，再
   单独提供兼容模式。Geth compatibility Adapter 保持现状，不把两者混为一谈。
2. **degenerate error 成员语义（2.3）**。code 0/"json-rpc error 0" 的错误被
   pool 视为可用答案并计健康成功。这是 Geth 解码行为泄漏出来的事实，不应成为
   自有 Adapter 的规范。建议自有 Adapter 将非对象、缺少 code/message 或无法
   形成合法 JSON-RPC error 的成员归为 malformed response；若要兼容旧 provider，
   通过明确的 compatibility mode 处理，并单独评估 health 影响。
3. **HTTP Close 的契约（2.6）**。Geth 现状是 no-op、不中止在途请求、close 后
   仍可调用。建议自有 Adapter 的 Close 幂等、关闭空闲连接并拒绝新调用；在途
   调用是否取消由 pool 的 context/lifecycle 契约决定，不能依赖 Geth 的 no-op。
4. **request ID 规则（2.1）**。自有 Adapter 的规范约束是每个 client 内并发安全、
   唯一、可序列化为 JSON-RPC ID；起始值与单调性不是公共语义。可以复刻 Geth
   的从 1 开始计数，但不应让上层依赖它。
5. **错误形态的具体类型（2.2）**。`io.EOF`、`*json.SyntaxError`、
   `gethrpc.ErrNoResult` 等具体类型目前被 baseline 测试断言，但生产上层只
   依赖"失败且非 ProtocolError"。建议：自有 Adapter 可用自有错误类型，但
   语义（失败、非可用答案、不声称 presence）必须等价；若保留 geth 类型则
   差分面为零。
6. **204/空 body 的形态（2.2/2.4）**。与第 5 点同理：必须失败，是否把
   `io.EOF` 换成自有"空响应"错误由裁决定。

7. **官方类型与自有 RPC 实现的边界**。自有 Adapter 可以替换 `rpc.Client`、
   `ethclient.Client`、JSON-RPC codec 和 HTTP/WSS lifecycle；`common`、`hexutil`、
   `core/types` 以及公开 `ethclient-shaped` 方法的类型仍遵从 go-ethereum 官方
   定义。类型兼容和连接实现解耦，不要求移除 go-ethereum module。

### EthGuard 自有 HTTP contract

在实现自有 Adapter 时，以下是 EthGuard 的规范契约；它优先于 Geth v1.17.5 的
B 类行为：

1. **Wire envelope**：发送 JSON-RPC 2.0 request；`params == nil` 时省略成员，
   非 nil 空切片发送 `[]`；每个请求只对应一个 response。
2. **Response validation**：必须校验 `jsonrpc == "2.0"`、response ID 与 request
   ID 匹配、`result` 与 `error` 互斥。违反时返回 malformed transport error，
   不生成 `ProtocolError`，也不写入 error-data presence。
3. **Result semantics**：`result: null` 是成功的 raw JSON `null`；缺失 result
   是 malformed/empty-result failure，不是可用答案。
4. **Error semantics**：合法 error 必须有整数 code 和字符串 message；data 可选，
   但必须保留 absent、JSON null、具体 JSON value 三态。Adapter 返回项目自有
   `ProtocolError`、`DataError`、`ErrorDataPresence` capability。
5. **HTTP status**：非 2xx 返回项目自有 `HTTPStatusError`，保留有限 body；不将
   非 2xx body 解析成 `ProtocolError`。429 可实现 `RateLimitError`。
6. **Limits and cancellation**：所有 response body 都受有界读取保护；context
   cancellation/deadline 优先于 body limit 和 read failure；不自动 retry、重放或
   猜测未知写结果。
7. **Lifecycle**：Adapter Close 幂等、阻止新调用并可关闭 idle connections；在途
   调用的完成或取消由 context/lifecycle 规定，pool 关闭后的结果必须丢弃并返回
   `ErrClosed`。
8. **Request IDs**：只要求并发安全、唯一和可序列化为合法 JSON-RPC ID；不让上层
   依赖 Geth 的起始值或单调性。

这组规则让实现可以替换 `go-ethereum/rpc` 和 `ethclient`，同时继续使用
go-ethereum 的 `common`、`hexutil`、`core/types` 和公开 ethclient-shaped 类型。
官方库成为类型与生态兼容来源，不再成为连接实现的稳定性来源。

### §4 附：七个特别问题的直接回答

- **是否需要复制 Geth HTTP 的错误链**：复制行为与 capability，不复制具体
  类型。grep 证据：`gethrpc` 错误类型在 `internal/transport` 之外的生产引用
  为零（`ethadapter` 只用 `gethrpc.BlockNumberOrHash` 请求参数类型）；
  `upstream.IsClientQuit(gethrpc.ErrClientQuit)` 在 HTTP 路径永不触发，自有
  Adapter 不产生该哨兵即等价。
- **HTTP Close no-op 与 Source close 的关系**：Geth baseline 的 Adapter Close
  是 no-op；pool 的 Source close 语义完全由 pool 自己的 closed gate 与
  `collectOne` 的 isClosed 检查提供。自有 Adapter 不必复制 no-op，但必须保持
  pool 关闭后的拒绝和在途调用的明确 context/lifecycle 语义。
- **body capture 是否属于 Adapter 内部**：是。capture 的全部机制（box、
  RoundTrip 包装、presence 恢复）都在 `internal/transport` 内，seam 上唯一
  可见产物是错误值实现 `ErrorDataPresence`。自有实现应整体重做该机制，不得
  把 raw body 暴露到 Transport 接口；"一个 call context 恰一个响应体"的
  单跳假设需原样保留或加 per-response 归属（见 `rawbody.go` 注释）。
- **自有 Adapter 是否必须保留 error.data presence**：必须（HTTP 路径）。
  `aggregator.observationRPCError` 以 presence 为第一优先、DataError 为回退；
  删除 presence 即把 HTTP 降级到 WSS 的"未知 presence"世界，是行为回退。
- **是否需要连接复用**：需要"同 client 复用、不逐调用拨号"（pool 的 Source
  缓存已强制一次 dial）；TCP keep-alive 用 net/http 免费获得。唯一注意点：
  body 必须读尽连接才可复用（capture 路径已读全 body，天然满足）。
- **是否需要完全复刻 Geth 的 request ID 规则**：见裁决 4（建议复刻，成本
  为零；硬约束只是唯一性）。
- **是否存在必须改变 Transport 接口才能兼容的行为**：不存在。已审计行为
  全部落在 `CallContext/ChainID/Close` 之上：capture/presence 走错误值，
  body limit 是 Adapter 内部，Close 的 no-op 语义由 pool 层补足，ChainID
  可自行实现 hex→big.Int 解码。若未来要暴露"按响应匹配 ID"或多响应归属，
  应新增 capability 而非改方法签名。

## 5. 自有 HTTP Adapter 的实现成本与主要风险

成本分布：

- **低成本**：POST envelope 与固定 headers；ID 计数器；非 2xx →
  HTTPError 等价物 + capability 包装；连接复用（直接用 net/http）；204/空
  body/malformed 的失败路径。
- **中等成本**：响应解码全形态（result null、缺 result、degenerate error、
  error+result 并存）与"健康/可用答案"分类逐行对齐；presence 恢复管线
  （capture、单槽不变量、三层优先级）。
- **需要测试驱动的部分**：全部 baseline 测试当前断言 geth 具体错误类型；
  切换自有 Adapter 时需按 §4 裁决 5/6 把断言改写为语义等价形式，然后逐行
  差分。

主要风险：

1. **文本敏感的分类规则**。pool 的限流判定有文本分支（"rate limit"/"too
   many requests"/"throttl"），而非 2xx 的 body 全文在错误消息里。自有实现
   改变消息拼接（如丢掉 body 或截断）会静默改变限流归类。
2. **degenerate 形态的涟漪**。对 Geth compatibility baseline 的 degenerate error
   成员做任何规范化都会改变 health 与可用答案判定；自有 Adapter 若采用严格
   malformed 规范，必须把它写进独立契约并提供对应 health 测试（§4.2）。
3. **presence 管线的归属假设**。单槽 box 假设一个调用恰好一个响应体；若
   自有实现引入重定向跟随或内部重试，presence 可能声称错误归属的 body。
   禁止在自有实现里加入自动重试/重放（与本次 slice 禁令一致）。
4. **响应匹配收紧的回归**（§4.1）。
5. **传输栈漂移**。强制 HTTP/2、修改 keep-alive 参数或空闲超时会改变 RTT
   分布（health/调度输入）与连接复用基线；保持默认 net/http 行为可避免。
6. **Geth 版本升级**。本矩阵绑定 v1.17.5；升级前应重跑 baseline 测试并
   diff 本文矩阵（风险已在 transport-seam-migration.md §5 登记）。

## 6. 自有 HTTP Adapter 执行计划

### Slice 2a：project-owned wire decoder

- 新增 `internal/rpcwire`，只处理 JSON-RPC 2.0 envelope、ID、result/error、
  malformed response 和 error-data presence；不导入 `ethclient`、Geth `rpc` 或
  Ethereum object types；
- 先写 table/fuzz tests，覆盖 contract 的所有正负路径；
- 生成项目自有错误和 capability，不暴露 Geth concrete error。

### Slice 2b：native HTTP Adapter

- 新增 `internal/transport/http`，使用 `net/http.Client` 和受控连接复用；
- 实现 request ID、context、body limit、HTTP status、Close、ProtocolError 和
  presence；
- `ChainID` 通过普通 JSON-RPC call 和项目-owned quantity decoder 实现；
- 与现有 Geth Adapter 并列，暂不改变默认选择。

### Slice 2c：差分和接入

- 用本矩阵的 A 类语义和 EthGuard contract 对照 native Adapter；B 类差异必须
  明确记录，不得为了通过 baseline 重新引入 Geth quirks；
- 运行 pool/aggregator/livecanary 全量测试、race、vet 和 provider evidence；
- 主维护者审查通过后，才决定是否切换 HTTP 默认 Adapter；
- WSS 复用 `rpcwire`，另立 Slice 3，不在 HTTP 实现中顺便加入。

### 停止条件

- 需要扩大 `Transport` 接口；
- 需要修改 `Emission`、`Observation`、`RPCError`、Decision 或 policy；
- 为兼容 Geth 而放弃 response ID/version 校验或 malformed rejection；
- 引入自动 retry、重放、订阅、filter 或隐式 fallback；
- 发现 provider evidence 与规范 contract 冲突但没有主维护者裁决。

## 7. 本次新增/引用的测试

新增（`internal/transport/geth_http_baseline_test.go`，全部 loopback）：

- `TestHTTPBaselineRequestEnvelope`（无参/有参/空切片 wire 字节 + POST/headers）
- `TestHTTPBaselineRequestIDIsMonotonicCounter`
- `TestHTTPBaselineConcurrentCallsAreIndependentPOSTs`
- `TestHTTPBaselineNormalResponseForms`（result null / 缺 result）
- `TestHTTPBaselineEmptyBodyAndStatus204AreEOF`
- `TestHTTPBaselineMalformedBodySurfacesJSONError`（SyntaxError / UnmarshalTypeError / ErrUnexpectedEOF）
- `TestHTTPBaselineResponseIDAndVersionNotValidated`
- `TestHTTPBaselineErrorDataVariants`（对象/数组/数字/字符串/null/缺失）
- `TestHTTPBaselineErrorMemberDegenerateForms`
- `TestHTTPBaselineNon2xxBecomesHTTPErrorWithBody`（400/401/403/404/500）
- `TestHTTPBaseline429CarriesRateLimitCapability`
- `TestHTTPBaselineBodyExactLimitSucceeds`（恰达上限 / 超一字节）
- `TestHTTPBaselineDeadlineExceededDominates`
- `TestHTTPBaselineHTTPCloseIsNoOp`（在途不被中止 + close 后可继续调用）
- `TestHTTPBaselineConnectionReuseAcrossCalls`

引用的既有锁定：`rawbody_test.go`（presence 三态、超限、读失败、cancel 优先级、
恢复、单槽不变量）、`transport_test.go`（status/rate-limit capability）、
`pool/*_test.go`（seam 契约、健康记账、限流计数、gate、caller 取消）。

## 7. 验收记录

在 Slice 1d 工作树上执行（基线 `3781b93` + 本文档 + baseline 测试，无其他改动）：

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/transport ./pool -race -count=1` | ok（两包均通过） |
| `go test ./... -count=1` | exit 0，33 个包 ok |
| `go test ./... -race -count=1` | exit 0，33 个包 ok |
| `go vet ./...` | exit 0，无发现 |
| `gofmt -l .` | 无输出 |
| `git diff --check` | 无输出 |

本文档只描述审计结论，不改变任何生产行为。

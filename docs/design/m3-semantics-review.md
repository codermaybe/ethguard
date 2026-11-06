# M3 交易、收据、日志与执行语义审计

状态：**语义审计完成，公共接口未修改（2026-11-07）**。

本文是高推理轨给 Flash 和 R0 的交接材料。审计范围为
`eth_getTransactionReceipt`、`eth_getBlockReceipts`、`eth_getTransactionByHash`、
`eth_getLogs`、`eth_call` 和 `eth_estimateGas`，同时核对 root `Client`、
`EthClient` 视图、RPC method plan、typed projection、错误分层、请求-应答绑定、
HTTP/WSS request/response seam 和现有测试。没有修改 `eth.go`、
`ethclient_view.go`、`CallResult` 或生产 API。

## 现有事实

每个方法都经过同一条执行链：请求准备/必要的 reference 建立、source fan-out、
method-specific admission、resolver、canonical Value，再由 typed projection 投影
为 go-ethereum 类型。`CallResult.Decision` 保存已经形成的事实；业务错误放在
`CallResult.Err`。因此“有 error”不一定意味着 Decision 被抹掉：例如 corroborated
receipt-null 仍保留 `Status=Corroborated` 和 `Value=null`，而 typed facade 把它映射成
`ethereum.NotFound`；corroborated `eth_call` revert 仍保留裁决并返回 `*RevertError`。

### 方法矩阵

| 方法 | resolver / reference | null、空值与 pending | 绑定与内容完整性 | 当前 typed 结论 |
| --- | --- | --- | --- | --- |
| `TransactionReceipt` | strict；hash 直读，无 anchor | `null` 是“当前没有 receipt”的可裁决业务事实；root/view 映射 `ethereum.NotFound`。没有独立 pending 标志，pending 与 absent 在该 RPC 上不可区分。空对象、缺必填字段、错误类型属于 malformed。 | receipt 的 `transactionHash` 必须等于请求 hash；receipt 必填 inclusion/gas/log 字段；每个 log 的非零 `transactionHash`/`blockHash` 必须绑定 receipt；`logsBloom` 必须等于解码后 logs 的 `CreateBloom`。 | 成功返回 `*types.Receipt`；错误仍带 Decision。receipt-null 不应被改成空 receipt 或零值。 |
| `BlockReceipts` | strict；显式 number/hash；moving tag 先 pin 到 `decision.BlockRef`；pending 在 plan 层 local reject | `null` 表示 block 不存在，view 映射 `ethereum.NotFound`；`[]` 是合法空 block。pending 不访问 provider。数组顺序、数组长度和元素内容都是比较值；不能把“至少这些 receipt”当成完整性证明。 | hash 请求逐 receipt 绑定 `BlockHash`；number 请求逐 receipt 绑定 `BlockNumber`；moving tag 绑定已 pin 的 hash；每个元素重复 receipt 完整性、log binding、bloom 检查；一个坏元素使整个 source 无效。 | 空数组成功；跨 source null/array 或数组排序差异是 conflict。大数组当前按完整 JSON/切片解码，没有专门的大小预算或分页语义。 |
| `TransactionByHash` | strict；hash 直读，无 anchor | `null` 映射 `ethereum.NotFound`。对象没有 `blockNumber` 时返回 `pending=true`；有 inclusion context 时返回 false。pending 是 source 观察到的 blockless payload，不是额外的共识结论。 | typed decoder 重算签名 hash 并与 wire `hash` 比较；有 `blockHash` 就必须同时有 `blockNumber` 和 `transactionIndex`；缺签名、缺 sender、unsupported tx type 或请求 hash 不匹配均 malformed。 | 返回 `(*types.Transaction, pending, CallResult, error)`；混合 null/object 或不同 payload 走 strict conflict，不应静默选择 pending。 |
| `FilterLogs` | strict；height fields anchor；`blockHash` 直读；pending range local reject | `null` 不是空结果而是 malformed；`[]` 是合法成功。nil bounds 默认到 anchored height；`fromBlock/toBlock` 含 pending、非法类型或逆序在本地 `ErrInvalidRequest`，不建立 reference、不 fan-out。 | 逐 log 必填字段和 wire shape；`blockHash` filter 逐项绑定 hash；range filter 逐项检查 `blockNumber` 在范围内；`removed` 必须是 bool；`blockTimestamp` 可缺省/null，存在时 canonical quantity；数组顺序与重复项保留。 | 结果为 `[]types.Log`。`removed` 是单次 Pull 的 provider 字段；没有订阅、reorg、缺口或 exactly-once 语义。 |
| `CallContract` | call-revert resolver；nil/latest 先建立 reference 并改写为 hash；pending 允许 local observation | 成功结果必须是 hex DATA；JSON null 成功值 malformed。执行 revert 是业务观察：code 3 或带 revert wording 的 -32000 进入 revert 维度，非 revert provider error 不进入执行比较。missing、JSON null、`"0x"`、非空 hex、结构化 data 是不同 key。 | 根 facade 将 typed revert 映射为 `*RevertError`，`Data` 只对 hex 字符串赋值，`DataRaw` 保留结构化 JSON；执行值/错误混合 cluster 时不把 provider error 当成功值。 | 同一 call 可 `Decision.Status=Corroborated` 且 `Err=*RevertError`；具体 revert data 不能获得足够支持时被 withholding/reductive projection，不能从另一 source 拼接。 |
| `EstimateGas` | median resolver；nil block 参数固定为 `latest` 后 pin/rewrite；plan 声明 pending local observation，但 facade 目前只暴露 latest 形态 | 仅接受 quantity；JSON null、错误对象、overflow/非法 quantity 属 malformed 或 source fault。所有 source 都以 execution failure 回答时是 no-valid-response；revert 不应变成 gas 数字。结果可为 `Selected`，不声称 corroborated。 | call object wire shape 与 `CallContract` 共用；request 需使用同一 pinned reference。resolver 使用 lower median，故不同 source 的合法估值不构成 strict conflict。 | root 返回 `(uint64, CallResult, error)`，view 投影为 `(uint64, error)`。没有 `EstimateGasAtBlock`/pending facade，不能从当前方法推导出完整的官方 pending 语义。 |

### receipt、log 和 bloom 的共同边界

receipt 与 `eth_getLogs` 共用 canonical log object。canonical encoder 会丢弃方法
比较掩码之外的 vendor 字段，但不会默认补齐缺失字段。receipt 的 `status` 或
`root` 至少一个必须存在；`logs` 必须是数组；`null` log element、非零 foreign log
hash、bloom 与 canonical logs 不一致都会令该 source 留在 denominator 中并记录 fault，
不能因为其他 source 足够而缩小分母。空 logs 数组和空 block receipts 数组是合法业务值。

receipt 的 log binding 是“响应内部自洽性”检查，transaction/block request binding
是“响应回答了哪个请求”检查；两者不能互相替代。`BlockReceipts` 的逐元素绑定
同样只证明每个返回元素属于请求 block，不证明 provider 返回了 block 的全部 receipt。

主要证据位置：`typed_reads_test.go`（receipt、transaction、block receipts、call、gas、
logs 的 facade 和 request binding）、`typed_receipt_canonical_test.go`（receipt canonical
fields、timestamp、removed、block-receipt array）、`typed_logs_pending_test.go`（pending
range local reject）、`typed_call_revert_4e_test.go` 与 `typed_call_boundary_test.go`
（revert data presence 和 withholding）、`typed_block_receipts_pending_test.go`（pending
block receipts no-wire），以及 `rpc/methods/eth_gettransactionreceipt_test.go`、
`eth_getblockreceipts_test.go`、`eth_getlogs_test.go`、`eth_call_revert_test.go` 的
policy/admission 反例。

## 结果、错误与材料矩阵

| 情况 | Decision | typed/root error | View error | 证据处理 |
| --- | --- | --- | --- | --- |
| receipt/transaction/block receipts 全部 `null` 且达到 quorum | `Corroborated`, `Value=null` | `ethereum.NotFound` | `ethereum.NotFound` | Support、Representative、原始材料保留；不是 `ErrNoValidResponse` |
| `null` 与 object/array split | `Unknown`，通常 `ErrConflict` | 同左 | 同左 | Reject/Support 和 conflict diagnostics 保留 |
| unanimous malformed object/array | `Unknown`，或已形成决策后 typed projection 失败 | `ErrMalformedResponse`（含具体缺失/绑定/bloom 原因） | 同左 | 失败 source 计入 denominator，Faults 不被删除 |
| logs `null` / call success `null` / gas `null` | malformed | `ErrMalformedResponse` | 同左 | 不折叠成空 slice、空 bytes 或 zero gas |
| corroborated call revert | `Corroborated`，revert/data 维度单独记录 | `*RevertError` | `*RevertError` | `RevertContext` 与 withheld data 属材料，不把 message 当 protected Value |
| estimate gas 合法但 source 数值不同 | `Selected`（lower median） | nil | nil | Reject/Support 记录；不宣称 cross-source equality |
| local pending/range rejection | `Unknown`，无 Support | `ErrInvalidRequest` | 同左 | 不建立 anchor、不向上游发请求 |

## transport 审计

当前 native HTTP 与 raw WSS 共用 `pool.Transport`、JSON-RPC request encoder、
response codec 和 method execution；WSS 只增加 Upgrade、持久连接和 request-id
demux，不应增加 M3 业务分支。geth compatibility adapter 是显式 HTTP adapter：
请求字节应与 native HTTP 一致，但其单次调用按位置配对响应，允许 native strict
codec 拒绝的缺失/不匹配 ID。typed M3 结果不能依赖这个 envelope 差异。

现有 `typed_transport_matrix_test.go` 只覆盖 `ChainID` 和 `HeaderByNumber`；
M3 的 receipt、block receipts、transaction、logs、call、estimate 尚无同 fixture
下的 HTTP/WSS/gethcompat 证据。该测试在本沙箱执行时失败于
`transport connections = http:0 wss:1`，说明 HTTP peer 未建立连接；WSS 请求已到达。
这不是 M3 值/错误语义的通过证据，应作为 T0 fixture/lifecycle 问题保留。

对 M3 来说，跨 transport 必须至少断言：

* method name、参数数量/顺序、call object 字段省略规则和 block reference 改写完全相同；
* raw `null`、empty array、revert `error.data` 的 presence（missing/null/value）不被 codec 改写；
* response request-id、连接关闭、取消和 owner `Close` 不改变已形成的 Decision；
* gethcompat 的 envelope 宽松性只能影响 transport admission，不能改变 typed projection 或错误类别。

真实 provider 的 `error.data=null` golden 仍未观察到；现有 revert presence 结论来自合成 fixture 和离线 policy tests，不能写成 provider-backed 事实。

## 可交给 Flash 的机械任务

这些任务只补已有契约的 fixture、薄 facade 回归和覆盖文档，不决定新结果类型或公共错误：

1. 扩展 T0 transport matrix 的 source peer/HTTP differential fixture，逐项覆盖
   `TransactionReceipt`、`BlockReceipts`、`TransactionByHash`、`FilterLogs`、
   `CallContract`、`EstimateGas`；固定请求参数、raw response 和 response-id 断言。
2. 增加 root/view 对照回归：receipt/block null→`ethereum.NotFound`、transaction
   pending bool、logs null→malformed、call revert→`RevertError`、estimate lower median。
   只复用现有 `eth.go`/`ethclient_view.go` 执行入口，不改签名。
3. 扩展已有 typed fixtures：receipt bloom/log binding、block receipts 每元素 hash/height
   binding、空数组、foreign block、transaction hash/signature/inclusion context、logs
   range/blockHash binding、call data presence 四态、gas malformed/overflow。
4. 为 WSS/native HTTP/gethcompat 增加 Close、cancel、request-id demux 和 raw error-data
   preservation 测试；失败保留日志，不把 transport 失败改成业务成功。
5. 更新 `method-transport-coverage.md`、T0 matrix 和方法卡片，记录实际命令、失败
   fixture、`not_observed` provider 事实。文件白名单应限于对应测试、覆盖文档和 fixture。

Flash 不应修改 `CallResult`、`ResultCapture`、公共 facade 签名、aggregator/reference
seam，也不应选择 receipt null、pending 或 revert data 的新业务语义。

## 必须进入 R0 的问题

1. **receipt null 的业务含义**：同一个 `null` 同时覆盖“交易尚未 mined”和“节点不知道
   该交易”；当前 API 只能映射 `ethereum.NotFound`。是否需要 pending/absent 的新
   结果事实、source-local 诊断或轮询契约，不能由单方法 wrapper 决定。
2. **BlockReceipts 完整性与资源边界**：`[]` 合法但不证明 block 完整；当前大数组一次
   materialize/canonicalize/decode，没有分页、元素数、字节预算或 partial-result API。
   R0 需决定预算失败是 transport/resource error 还是业务 error，并保持空 block 语义。
3. **TransactionByHash pending 的裁决**：pending bool 来自获胜 payload 的 inclusion
   fields。需要明确缺 field、null field、source 间 mined/pending split、reorg 后再次
   读取的规则，以及是否仍可将 bool 视为 `Corroborated` 事实。
4. **FilterLogs 的 moving/pending/reorg 边界**：当前 pending range local reject，
   `removed` 只是 Pull 字段；是否支持 pending logs、reorg/removed 的生命周期、超大
   range 分段和结果去重，必须与未来订阅契约统一。
5. **CallContract / PendingCallContract**：当前 `eth_call` 将 revert 作为
   `Corroborated + RevertError`，并对 data 做 reductive withholding；需要决定
   structured error data、provider error code、pending execution 的 source-local 与
   quorum 规则，以及是否新增 `CallContractAtHash`/`PendingCallContract`，不能局部扩 facade。
6. **EstimateGas 语义**：lower median 是选择值而非 corroborated value；需要冻结
   all-revert、部分 source error、zero/overflow、pending/at-block 形态和是否暴露
   block-specific estimation。不能把 gas 失败复用 call 的 `RevertError` 而不做统一设计。
7. **transport envelope 与错误 presence**：native/gethcompat 对 ID 和 malformed
   envelope 的差异应继续停留在 transport 层；若某 provider 把 `error.data` missing、
   null、empty 混写，需 R0 明确是否扩大 `CallResult`/error carrier，而不是在 Flash
   fixture 中静默归一化。

## 定向验证记录

通过：

```text
go test . -run 'Test(CanonicalReceipt|CanonicalBlockReceipts|ReceiptNullLog|TypedTransaction(ByHash|Receipt)|TypedBlockReceipts|TypedCallContract|TypedEstimateGas|TypedFilterLogs|LogsPending|CallRevert)' -count=1
go test . -race -run 'Test(CanonicalReceipt|CanonicalBlockReceipts|ReceiptNullLog|TypedTransaction(ByHash|Receipt)|TypedBlockReceipts|TypedCallContract|TypedEstimateGas|TypedFilterLogs|LogsPending|CallRevert)' -count=1
go test ./rpc/methods -run 'Test(Receipt|BlockReceipt|Logs|Call|Estimate|Transaction)' -count=1
go test ./rpc/methods -race -run 'Test(Receipt|BlockReceipt|Logs|Call|Estimate|Transaction)' -count=1
go test ./internal/typed -run 'Test(Receipt|Call|Transaction|Log)' -count=1
go test ./internal/verification/integration -run 'TestVerificationMethodScenario(GetTransactionReceipt|GetTransactionByHash|GetBlockReceipts|EstimateGas|GetLogs)|TestVerificationMethodScenarioGetLogsNullIsMalformed' -count=1
go test ./internal/verification/integration -race -run 'TestVerificationMethodScenario(GetTransactionReceipt|GetTransactionByHash|GetBlockReceipts|EstimateGas|GetLogs)|TestVerificationMethodScenarioGetLogsNullIsMalformed' -count=1
```

已知失败（fixture/lifecycle 问题，未改动）：

```text
go test . -run '^TestTypedTransportMatrix$' -count=1
--- FAIL: TestTypedTransportMatrix
    typed_transport_matrix_test.go:54: transport connections = http:0 wss:1
```

该失败只说明当前跨 transport smoke fixture 没有观察到 HTTP 连接；不能用它关闭 M3
transport 缺口，也不构成公共 API 变更理由。

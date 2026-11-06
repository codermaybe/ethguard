# M3 大数组、材料预算与生命周期审计

状态：**审计完成，未修改公共接口、聚合器或预算模型（2026-11-07）**。

本审计核对 `eth_getTransactionByHash`、`eth_getTransactionReceipt`、
`eth_getBlockReceipts`、`eth_getLogs`、`eth_call` 和 `eth_estimateGas` 的资源边界、
证据保留、取消/关闭和 malformed/null/NotFound 行为。审计把“响应可以被业务解码”
和“响应的原始材料可以被保留”分开；证据预算耗尽不能改变请求、quorum、Decision 或
typed 结果。

## 已冻结的资源边界

一份上游 JSON 响应先经过 `internal/jsonsafe`，单体响应上限为 `32 MiB`，嵌套深度为
32，对象键数为 4096，并拒绝重复键和尾随数据。这个限制适用于 receipt、block receipts、
logs、transaction 和 call/error 数据；它是响应级边界，不是数组元素级预算。

启用 `WithSourceEvidence` 后，单次 public call 共享一个 invocation-scoped 材料预算：

| 项目 | 当前上限/规则 | 影响 |
| --- | --- | --- |
| 总保留材料 | 调用方配置的正整数；默认材料入口为 1 MiB | reference、business fan-out、admission 和 broadcast 共用；不同调用不共享 |
| 单条 raw response | 64 KiB | 超出部分保留前缀并标记 `Truncated` |
| 记录总数 | raw 与 fields 合计 256 条 | 超限停止保留并增加 `Dropped` |
| 单个可变元数据文本 | 4 KiB | stage、method、source identity 和 error text 各自截断 |
| 预算耗尽 | 只停止材料保留 | 不取消请求、不缩小分母、不改变业务错误或结果 |

原始响应和字段展开分别计费；字段展开只扫描已经保留的 raw 前缀，不继续解析被截断
的尾部。材料快照在 `CallResult.Uncertain` 和 `ResultCapture` 交付时复制，调用方修改
快照不会影响后续读取。`ResultCapture` 仍是一次调用句柄，复用会返回
`ErrCaptureReused`，不能把单次材料预算扩展成流式预算。

## 方法级成本与完整性事实

| 方法 | 当前数组/对象成本 | 已验证的异常边界 | 未承诺的能力 |
| --- | --- | --- | --- |
| `TransactionByHash` | 单对象受 32 MiB body、JSON 深度/键数和 typed transaction 解码约束 | `null` → `ethereum.NotFound`；pending 由缺少 inclusion 字段表示；foreign hash、缺签名、缺 inclusion 配套字段为 malformed | 不承诺 pending 与 absent 的区分、不承诺 mined 等待或 reorg 生命周期 |
| `TransactionReceipt` | receipt 与 logs 一次完整解码；logs 数组没有单独元素数或字节预算 | `null` → `ethereum.NotFound`；空 logs 合法；null log、缺字段、foreign binding、bloom 不一致为 malformed | 不证明交易尚未 mined 还是节点未知；不提供分页 receipt/log API |
| `BlockReceipts` | 整个数组一次 materialize、canonicalize 和 typed decode；每个元素重复 receipt/log/bloom 校验 | 顶层 `null` → `ethereum.NotFound`；`[]` 合法空 block；坏元素使来源无效并保留 denominator | 不提供数组分段、元素上限、partial result 或“返回完整 block receipt 集合”的证明 |
| `FilterLogs` | 整个结果数组一次 materialize/decode；响应仍受 32 MiB body 和 JSON 结构限制 | `[]` 合法；`null`、范围逆序、pending range、foreign block/hash binding 为 malformed 或 local invalid request | 不承诺超大范围自动分段、跨调用去重、reorg/removed 流语义或 exactly-once |
| `CallContract` | call result 通常为 DATA；revert error/data 由 response codec 与方法 admission 保留 | success `null` 为 malformed；revert 可形成 `Corroborated + RevertError`；data missing/null/empty/value 保持不同 presence | provider error-data 的真实分布尚未完成 provider-backed golden；不承诺 pending call quorum |
| `EstimateGas` | quantity 小对象；执行错误按 source fault/median 输入处理 | null、错误对象、overflow、非法 quantity、全源执行失败均有反例 | lower median 是 `Selected`，不代表共识；未提供 at-block/pending facade |

大数组当前没有独立的“业务资源错误”类型。达到 32 MiB、JSON 深度或键数边界时，
transport/JSON admission 失败；材料预算不足时只产生 `Uncertain.Truncated`/
`Dropped`。不能把材料截断误报为业务值不完整，也不能把 `[]` 改成 `NotFound`。

## 生命周期与取消

现有调用链在 context 取消和 owner `Close` 时停止 transport、pool 和 invocation worker；
已形成的 Decision 保留在返回值或 capture 中，未完成调用按原有 context/transport 错误
结束。材料 collector 的互斥保护覆盖并发 source 记录和 Snapshot；Snapshot 是防御性复制。
这些规则在 receipt、block receipts、logs、transaction、call 和 gas 的调用路径中共用，
没有方法专用的后台 goroutine 或隐式重试。

取消和 Close 不会把已形成的 `ethereum.NotFound`、`ErrMalformedResponse`、
`*RevertError` 或 `WriteDecision` 改成另一类业务错误。订阅尚未实现；不能把当前 unary
collector、`ResultCapture` 或 `WithSourceEvidence` 当作订阅材料队列。

## 证据与 provider 事实

已验证的离线/fixture 事实包括：receipt null、block receipts 空数组、日志 null 与空数组
区分、逐元素 block/transaction binding、receipt bloom 校验、transaction pending flag、
call error-data presence 四态以及材料预算的截断/记录上限。相关证据位于
`typed_receipt_canonical_test.go`、`typed_reads_test.go`、`typed_logs_transport_test.go`、
`typed_call_revert_4e_test.go`、`internal/uncertain/*_test.go` 和
`internal/jsonsafe/*_test.go`。

真实 provider 尚未观察到 `error.data = null` 的稳定 golden；当前 data presence 结论来自
wire/policy/合成 fixture，必须继续标记为 `not_observed`。同样没有 provider-backed 证据
证明某个节点在超大 `eth_getLogs` 或 `eth_getBlockReceipts` 响应上会分页、截断或返回特定
资源错误；不得把本地 32 MiB admission 结果写成 provider 行为。

## F0/R0 升级触发器

以下任一事实出现时，必须交回 R0/F0，不能由方法 agent 局部修复：

1. 需要为数组元素数、总字节数、分页或 partial result 增加新的公共结果/错误类型。
2. 需要把材料预算耗尽映射成业务失败、降低 quorum、改变 Decision 或清空 typed Value。
3. 需要区分 receipt `null` 的 pending 与 absent，或为 transaction pending 增加跨 source 一致性语义。
4. 需要支持 logs pending、reorg/removed、超大范围分段、跨调用去重或订阅补查。
5. provider 的 `error.data` missing/null/empty/value 形态要求改变 `RevertError` 或 `CallResult`。
6. 取消、Close、重试或 owner 生命周期需要后台任务、异步错误或新的材料队列。

## 验收命令

```text
gofmt -l .
git diff --check
go test . -run 'Test(CanonicalReceipt|CanonicalBlockReceipts|TypedTransaction(ByHash|Receipt)|TypedBlockReceipts|TypedFilterLogs|TypedCallContract|TypedEstimateGas|LogsPending|CallRevert)' -count=1
go test . -race -run 'Test(CanonicalReceipt|CanonicalBlockReceipts|TypedTransaction(ByHash|Receipt)|TypedBlockReceipts|TypedFilterLogs|TypedCallContract|TypedEstimateGas|LogsPending|CallRevert)' -count=1
go test ./internal/uncertain ./internal/jsonsafe ./internal/limits -count=1
go test ./internal/uncertain ./internal/jsonsafe -race -count=2
```

上述命令只验证已冻结的 unary 事实；它们不构成大数组分页、provider 资源行为或订阅
exactly-once 的承诺。

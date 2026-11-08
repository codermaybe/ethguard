# 方法与传输覆盖清单

状态：**当前 v1.17.10 完整兼容矩阵（2026-11-09）；尚未达到完整 ethclient 发布门槛**。

本文是锁定 go-ethereum 版本的方法级实现与证据索引；当前缺失方法必须在完整兼容发布前关闭，不能从“Registered”或普通 HTTP/WSS request 能力推导为已支持。

本清单以仓库锁定的 go-ethereum `v1.17.10`、`rpc/methods` catalog/ReadPlan、根 `Client` 和 `EthClient` 视图为事实源。它只记录当前代码事实和缺口，不授权单个方法任务修改公共接口。公共接口变更统一由 roadmap 管理。

### v1.17.10 方法差异基线

基线直接对锁定 module 的 `*ethclient.Client` 做 Go reflection，并与本仓库
`*EthClient` 的导出方法集合比较。结果是上游 48 个导出方法（其中
`Client()` 是底层 `*rpc.Client` 访问器）与当前 46 个方法；排除该访问器后的业务目标为
47 个，当前业务缺口为 1 个方法。
这只记录代码事实，不把部分方法解释为兼容版本。可复现命令为：

```text
scripts/ethclient-method-diff.sh
```

脚本内临时程序的核心是 `reflect.TypeOf((*ethclient.Client)(nil)).NumMethod()`
和 `reflect.TypeOf((*ethguard.EthClient)(nil)).NumMethod()`，并按方法名计算
集合差异。若上游版本变化，必须重新生成该差异后再更新本节、下表和锁定版本。

当前业务缺失集合为：

`SimulateV1`。其余 pending state、同步写入和三类订阅均已暴露 facade；仍需
逐项补齐跨传输 fixture、错误和证据矩阵，暂不代表最终兼容发布已闭合。
`gethcompat.SimulateClient` now provides an opt-in single-source adapter with
the official `ethclient.SimulateOptions` and `SimulateBlockResult` types. This
adapter is an explicit compatibility escape hatch; it is not a Root/View
mapping and does not reduce the business gap while the default EthClient
surface remains evidence-backed.

`Client()` 不是业务 RPC 方法，明确排除在 `EthClient` facade 之外；它不能通过
忽略而把业务差异伪装成已闭合。该缺口必须给出 Root、typed、错误、
生命周期和 HTTP/WSS 证据，订阅方法还要满足独立订阅契约。

## 覆盖分类

- **Root**：根 `Client` 是否有带 `CallResult` 的原生方法。
- **View**：`Client.EthClient()` 是否提供官方 go-ethereum 形态的方法。
- **RPC**：是否有项目 catalog/ReadPlan 或 write pipeline 的明确执行路径。
- **HTTP/WSS**：现有 transport 是否有请求/应答能力；“WSS request”不是订阅。
- **Subscription**：是否有订阅能力。生产 facade 对 newHeads、logs 和
  transactionReceipts 支持固定 WSS source snapshot 的严格多数聚合；单源
  配置保留单源转发兼容路径（尚未开放 SingleSourceSelected 配置）。
  per-source 重连与迟到 generation 过滤已接入；newHeads、logs 和 receipts
  均有同源 Pull 并绑定物理 session。newHeads 的跳高 gap 会在同一 Pull
  窗口验证缺失高度；Pull 成功继续且不重放，无法补齐则通过结构化
  `SubscriptionGapError` 终止 `Err()`。logs/receipts 不凭空推导高度 gap；
  跨 transport 证据仍是后续缺口。
- **Evidence**：当前测试是否覆盖值、裁决、错误、材料和生命周期，而不仅是返回值。

## 项目 catalog 与现有切面

| RPC 方法 | Catalog | Root | View | HTTP/WSS request | Subscription | 主要实现 | 当前缺口 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `eth_chainId` | Registered | `ChainID` | `ChainID` | native HTTP/WSS raw call | 不适用 | `eth.go`, `ethclient_view.go`, `rpc/methods` | 跨 transport 同 fixture 结果矩阵 |
| `eth_getBlockByHash` | Registered | `BlockByHash` | `BlockByHash` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | full/header、uncle 和大区块成本 |
| `eth_getBlockByNumber` | Registered | `BlockByNumber` | `BlockByNumber` | native HTTP/WSS raw call | 不适用 | `eth.go`, `anchor.go`, `typed` | moving tag、pending-local、历史块跨 transport |
| `eth_getBalance` | Registered | `BalanceAt` | `BalanceAt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | null、历史高度和跨 transport |
| `eth_getTransactionReceipt` | Registered | `TransactionReceipt` | `TransactionReceipt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed`, `rpc/internal/objects/receipt` | 大数组/真实 provider 事实仍待补 |
| `eth_getTransactionByHash` | Registered | `TransactionByHash` | `TransactionByHash` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | pending 与 absent 的跨 transport 矩阵 |
| `eth_getBlockReceipts` | Registered | `BlockReceipts` | `BlockReceipts` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed`, receipt objects | 大数组、空 block、逐 receipt 绑定成本 |
| `eth_getTransactionCount` | Registered | `NonceAt` | `NonceAt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | pending-local、历史引用和跨 transport |
| `eth_getBlockTransactionCountByHash` | Registered | `TransactionCount` | `TransactionCount` | native HTTP/WSS raw call | 不适用 | `compat_methods.go`, `typed`, `rpc/methods` | 值/null/Conflict/Malformed 已有 facade 断言（`TestTypedTransactionCompatibilityTransportMatrix`、`TestTypedTransactionCompatibilityNullParity`、`TestTransactionCountMultiSourceConflictKeepsEvidence`、`TestTransactionCountMultiSourceMalformedFailsWithoutValue`）；跨 transport 负向 fixture 仍待补 |
| `eth_getBlockTransactionCountByNumber` | Registered | `PendingTransactionCount` | `PendingTransactionCount` | native HTTP/WSS raw call | 不适用 | `pending_count.go`, `typed`, `rpc/methods` | PendingObserve 的多源 dissent/malformed 已由 `TestPendingTransactionCountDissentStaysSelected`、`TestPendingTransactionCountSkipsMalformedSource`、`TestPendingTransactionCountAllMalformedIsUnknown` 覆盖；跨 transport 证据仍待补 |
| `eth_getStorageAt` | Registered | `StorageAt` | `StorageAt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 空 bytes、EIP-1898 和跨 transport |
| `eth_getCode` | Registered | `CodeAt` | `CodeAt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | empty code、历史引用和跨 transport |
| `eth_call` | Registered | `CallContract` | `CallContract` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed`, call rules | overrides、provider golden `error.data=null` |
| `eth_estimateGas` | Registered | `EstimateGas` | `EstimateGas` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | provider revert golden、边界参数 |
| `eth_getLogs` | Registered | `FilterLogs` | `FilterLogs` | native HTTP/WSS raw call | WSS 单源与多源 strict quorum | `eth.go`, `typed`, `subscription.go`, `internal/subscription` | 同源 Pull 已接入；跨 transport 证据和大范围成本仍待补 |
| `eth_feeHistory` | Registered | `FeeHistory` | `FeeHistory` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 历史高度、percentile 边界和跨 transport |
| `eth_gasPrice` | Registered | `SuggestGasPrice` | `SuggestGasPrice` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | selected 语义和异常来源 |
| `eth_maxPriorityFeePerGas` | Registered | `SuggestGasTipCap` | `SuggestGasTipCap` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | selected 语义和异常来源 |
| `eth_blobBaseFee` | Registered | `BlobBaseFee` | `BlobBaseFee` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | source 支持差异 |
| `eth_blockNumber` | Registered | `BlockNumber` | `BlockNumber` | native HTTP/WSS raw call | 不适用 | `eth.go`, `anchor.go` | reference 附带事实、跨 transport |
| `eth_createAccessList` | Registered | `CreateAccessList` | 不暴露 | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | extension 的官方生态边界 |
| `net_peerCount` | Registered | `PeerCount` | `PeerCount` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | source-local selected 语义 |
| `eth_syncing` | Registered | `SyncProgress` | `SyncProgress` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | syncing/null/对象差异；七个上游字段尚未投影，详见兼容政策 |
| `net_version` | Registered | `NetworkID` | `NetworkID` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | decimal identity 与 ChainID 关系 |
| `net_listening` | Registered | `Call` only | 不暴露 | raw dynamic read path | 不适用 | `call.go`, catalog | 没有 typed wrapper；需明确是否继续 Call-only |
| `eth_sendRawTransaction` | Write | `SendRawTransaction` | `SendTransaction` | native HTTP/WSS raw call | 不适用 | `write.go`, write pipeline | accepted/unknown/rejected 与 receipt 关系 |
| `eth_getProof` | Explicit unsupported | 不支持 | 不支持 | 不应 silent forward | 不适用 | catalog descriptor | 需要独立 proof admission/evidence contract |

项目 catalog 目前登记 25 个方法级条目，另有写入和显式 unsupported 条目。catalog 的注册不等于跨 transport、provider 或订阅证据已经完成。

## go-ethereum v1.17.10 官方 ethclient 方法

下面列出锁定版本 `ethclient.Client` 的公开方法。当前 `EthClient` 暴露 46
个方法；上游 `Client()` 只作为显式排除项记录。其余业务方法必须逐项接入并闭合
证据，在兼容发布前不能把“待接入”当作已实现。

| 官方方法 | 当前 View | Root/项目映射 | 分类 |
| --- | --- | --- | --- |
| `Close()` | 已有 | `Client.Close` | 生命周期 |
| `Client()` | 明确排除 | 无映射 | 上游基础设施访问器，不属于业务兼容面 |
| `ChainID` | 已有 | `Client.ChainID` | 已覆盖 |
| `BlockByHash` | 已有 | `Client.BlockByHash` | 已覆盖 |
| `BlockByNumber` | 已有 | `Client.BlockByNumber` | 已覆盖 |
| `BlockNumber` | 已有 | `Client.BlockNumber` | 已覆盖 |
| `PeerCount` | 已有 | `Client.PeerCount` | 已覆盖 |
| `BlockReceipts` | 已有 | `Client.BlockReceipts` | 已覆盖 |
| `HeaderByHash` | 已有 | `Client.HeaderByHash` | 已接入；header-only/fullTx=false 与 BlockByHash 共用 plan |
| `HeaderByNumber` | 已有 | `Client.HeaderByNumber` | 已接入；moving tags 复用 anchor，pending 本地拒绝 |
| `TransactionByHash` | 已有 | `Client.TransactionByHash` | 已覆盖 |
| `TransactionSender` | 已有 | `Client.TransactionSender` | 值/null/Conflict/Malformed 已有 facade 断言（`TestTransactionSenderMultiSourceConflictKeepsEvidence`、`TestTransactionSenderMultiSourceMalformedFailsWithFaults` 及既有 transport/null 测试）；sender 冲突的跨 transport fixture 仍待补 |
| `TransactionCount` | 已有 | `Client.TransactionCount` | 值/null/Conflict/Malformed 已有 facade 断言（`TestTransactionCountMultiSourceConflictKeepsEvidence`、`TestTransactionCountMultiSourceMalformedFailsWithoutValue`）；跨 transport 负向 fixture 仍待补 |
| `TransactionInBlock` | 已有 | `Client.TransactionInBlock` | 值/null/Conflict/Malformed 已有 facade 断言（`TestTransactionInBlockMultiSourceConflictKeepsEvidence`、`TestTransactionInBlockMultiSourceMalformedFailsWithFaults`）；inclusion envelope 跨 transport 负向 fixture 仍待补 |
| `TransactionReceipt` | 已有 | `Client.TransactionReceipt` | 已覆盖 |
| `SyncProgress` | 已有 | `Client.SyncProgress` | 已接入；仍遗漏七个上游同步/索引字段，剩余索引计数丢失会影响 `Done()`，详见兼容政策 |
| `NetworkID` | 已有 | `Client.NetworkID` | 已覆盖 |
| `BalanceAt` | 已有 | `Client.BalanceAt` | 已覆盖 |
| `BalanceAtHash` | 已有 | `Client.BalanceAtHash` | 已接入；EIP-1898 hash object |
| `StorageAt` | 已有 | `Client.StorageAt` | 已覆盖 |
| `StorageAtHash` | 已有 | `Client.StorageAtHash` | 已接入；EIP-1898 hash object |
| `CodeAt` | 已有 | `Client.CodeAt` | 已覆盖 |
| `CodeAtHash` | 已有 | `Client.CodeAtHash` | 已接入；EIP-1898 hash object |
| `NonceAt` | 已有 | `Client.NonceAt` | 已覆盖 |
| `NonceAtHash` | 已有 | `Client.NonceAtHash` | 已接入；EIP-1898 hash object |
| `FilterLogs` | 已有 | `Client.FilterLogs` | 已覆盖 |
| `SubscribeTransactionReceipts` | 已有（WSS 单源/多源） | `Client.SubscribeTransactionReceipts` | 固定 snapshot strict quorum、per-source reconnect、同源逐 receipt Pull；跨 transport 证据待补 |
| `SubscribeNewHead` | 已有（WSS 单源/多源） | `Client.SubscribeNewHead` | 固定 snapshot strict quorum、per-source reconnect、同源 Pull 与结构化 gap Err；跨 transport 证据待补 |
| `SubscribeFilterLogs` | 已有（WSS 单源/多源） | `Client.SubscribeFilterLogs` | fixed snapshot、removed/reorg、strict quorum、per-source reconnect、同源 eth_getLogs Pull；跨 transport 证据待补 |
| `PendingBalanceAt` | 已有 | `Client.PendingBalanceAt` | pending-local；跨 transport 证据待补 |
| `PendingStorageAt` | 已有 | `Client.PendingStorageAt` | pending-local；跨 transport 证据待补 |
| `PendingCodeAt` | 已有 | `Client.PendingCodeAt` | pending-local；跨 transport 证据待补 |
| `PendingNonceAt` | 已有 | `Client.PendingNonceAt` | pending-local；跨 transport 证据待补 |
| `PendingTransactionCount` | 已有 | `Client.PendingTransactionCount` | PendingObserve 多源负向已覆盖；跨 transport 证据待补 |
| `CallContract` | 已有 | `Client.CallContract` | 已覆盖 |
| `CallContractAtHash` | 已有 | `Client.CallContractAtHash` | EIP-1898/跨 transport 证据待补 |
| `PendingCallContract` | 已有 | `Client.PendingCallContract` | pending-local/错误证据待补 |
| `SuggestGasPrice` | 已有 | `Client.SuggestGasPrice` | 已覆盖 |
| `SuggestGasTipCap` | 已有 | `Client.SuggestGasTipCap` | 已覆盖 |
| `BlobBaseFee` | 已有 | `Client.BlobBaseFee` | 已覆盖 |
| `FeeHistory` | 已有 | `Client.FeeHistory` | 已覆盖 |
| `EstimateGas` | 已有 | `Client.EstimateGas` | 已覆盖 |
| `EstimateGasAtBlock` | 已有 | `Client.EstimateGasAtBlock` | 参数/错误/跨 transport 证据待补 |
| `EstimateGasAtBlockHash` | 已有 | `Client.EstimateGasAtBlockHash` | EIP-1898/跨 transport 证据待补 |
| `SendTransaction` | 已有 | `Client.SendRawTransaction` / view SendTransaction | 已覆盖 |
| `SendTransactionSync` | 已有 | `Client.SendTransactionSync` | broadcast+receipt polling；语义证据待补 |
| `SendRawTransactionSync` | 已有 | `Client.SendRawTransactionSync` | broadcast+receipt polling；语义证据待补 |
| `SimulateV1` | 明确阻塞 | 无映射（可选 `gethcompat.SimulateClient`） | 默认构建禁止导入 ethclient；可选适配器只提供单源官方类型调用，不属于 EthClient 兼容面 |

`go-ethereum/ethclient/gethclient` 还提供扩展方法，例如 `GetProof`、`CallContract` overrides、`Trace*`、`SubscribePendingTransactions` 和调试统计。它们不自动进入 EthClient；必须单独决定是否属于项目的生态承诺。

## 当前 transport 事实

| 能力 | native HTTP | native WSS | gethcompat | 备注 |
| --- | --- | --- | --- | --- |
| 普通 JSON-RPC request/response | 已有 | 已有 | 显式 adapter | 共用 `pool.Transport` seam |
| request ID demux | HTTP 请求级 | 已有 | 由 geth | WSS raw-call 已测试乱序和 pending 清理 |
| `ChainID` / `Close` | 已有 | 已有 | 显式 adapter | 共享 owner 生命周期 |
| `error.data` presence | 已有四态 | native raw 可保留；provider golden 未闭合 | 受 adapter 限制 | missing/null/value/unknown 必须分开记录 |
| 普通订阅 | 不适用 | 已有显式 capability；单 WSS source facade | 未承诺 | capability 不扩张 unary Transport；多源聚合已接入，Pull/gap/跨 transport 证据仍待补 |
| HTTP Pull 补查 | 可用 | 仅当同一 source 有配置 | 依 adapter | 不得把任意 HTTP endpoint 当作同一 source |
| reconnect / keepalive / queue | 未作为通用能力 | 未实现 | 不统一承诺 | 需订阅契约后单独决定 |

## 方法族执行顺序

1. 标量、费用、节点状态：确认 `Selected`、`Corroborated`、null 和 source-local 结果。
2. 区块与 reference：确认 block/header/full body、EIP-1898、moving tag、pending-local 和 reference 传播。
3. 交易、收据、logs、Call、EstimateGas：确认 NotFound、pending、revert、bloom、log binding 和范围。
4. 写入和扩展：确认 broadcast acceptance、unknown/rejected、CreateAccessList 生态边界。
5. HTTP/WSS 同 fixture：不改变业务执行核心，只补跨 transport 证据。
6. 订阅契约和实现：在上述事实闭合后进行，订阅使用独立事件模型。

## 任务准入与验收

每个方法任务必须提交：

- 方法卡片和覆盖矩阵更新；
- root 与 view 的调用/错误/Decision 对照；
- HTTP、WSS、必要时 gethcompat 的请求 fixture；
- null、malformed、conflict、cancel、Close 和证据预算反例；
- 实际命令和测试结果；
- 对公共返回值或订阅接口的影响分析。

方法任务不能因为一个 fixture 成功就标记完成。至少要分别记录：业务值、业务 error、裁决状态、reference、原始材料和生命周期。

当前基线命令：

```text
gofmt -l .
git diff --check
go test ./... -count=1
go test ./... -race -count=1
go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on
go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild' -count=1
```

订阅实现还必须增加取消、Unsubscribe、backpressure、reorg、重复事件、缺口、source snapshot 和 goroutine 泄漏测试；当前聚合器已覆盖固定 snapshot、strict quorum、重复事件、removed 绑定、有界队列、重连 session、newHeads gap 验证，以及 logs/receipts 的同源 Pull；跨 transport 证据仍待补齐。

## State-hash 方法当前状态

- `BalanceAtHash`、`NonceAtHash`、`StorageAtHash`、`CodeAtHash` 复用各自既有 ReadPlan、typed projection、请求冻结和执行入口；最后一个参数在 wire 上固定为 `{blockHash, requireCanonical:false}`。
- 根 Client 返回 `(T, CallResult, error)`，EthClient 视图返回 go-ethereum v1.17.10 的 `(T, error)`；四个 view 方法只投影同一 owner execution。
- 显式 hash 不建立 moving reference，也不走未知 `Call`；上游缺块/RPC 错误、冲突、解码错误和取消沿既有 Decision/错误/材料/Close 规则返回。

## 区块 Header 方法当前状态

- `HeaderByHash` 和 `HeaderByNumber` 复用 `eth_getBlockByHash` / `eth_getBlockByNumber` 的既有 ReadPlan、admission 和 block projector；只把 `fullTx` 固定为 `false`，再由 `DecodeBlockHeaderOnly` 交付 `*types.Header`。
- 根 Client 返回 `(* types.Header, CallResult, error)`，EthClient 视图保持官方 `(* types.Header, error)`。
- `HeaderByNumber(nil)`、`-2/-3/-4` 通过已建立 reference 读取并附带 reference 事实；`-5` 映射高度 `0x0`；`-1` 及未知负数在本地拒绝，不宣称 pending header 已支持。
- 定向测试：`typed_block_headers_test.go`、`typed_state_hash_test.go`、`ethclient_view_test.go` 方法集合会随锁定版本反射检查更新；当前已暴露 46 个方法，仍只是接入检查点；与上游 48 个导出方法的完整差异见本文件“v1.17.10 方法差异基线”。

## 未决项

- 官方 pending 族、交易派生方法是否进入 EthClient；
- `eth_getProof`、`SimulateV1`、gethclient 扩展是否属于项目承诺；
- root 订阅事件类型与官方视图订阅 channel 的关系；
- subscription 的 quorum、source membership、reorg/removed、Pull 补查和异步错误；其中 newHeads、logs、transactionReceipts 的固定 snapshot 与 strict quorum 已接入根 facade，三类订阅均已接入同源 Pull，newHeads 的跳高 gap 已按结构化 Err 终止，跨 transport 证据仍开放。
- 方法覆盖完成后，是否需要调整 `CallResult`、ResultCapture 或引入独立事件结果类型。

这些问题由 roadmap 统一收敛，不由单个方法 agent 局部决定。

## Pending 与 state-hash 当前状态

当前 pending 与 state-hash 语义如下：

- `BalanceAtHash`、`StorageAtHash`、`CodeAtHash`、`NonceAtHash` 在 go-ethereum v1.17.10 中仍调用现有四个 RPC 方法，只是把最后一个 block 参数编码成 EIP-1898 的 `{blockHash, requireCanonical:false}` 对象。
- 当前 `rpc/methods` 的 `ReferenceSpec{EIP1898:true}` 和 EIP-1898 rewrite 测试继续覆盖对象校验、显式 hash、`requireCanonical` 保留和 moving tag 改写；AtHash facade 通过同一 plan 执行。
- AtHash 接入使用受控的 EIP-1898 map 参数和现有 plan，不通过未知 `Call`。
- `PendingBalanceAt`、`PendingStorageAt`、`PendingCodeAt`、`PendingNonceAt`、`PendingTransactionCount` 的 plan 已有 `PendingObserve` 声明。pending 是节点本地状态，不能直接套用 moving reference；aggregator 可以收集多个 source，但结果只能是第一个可用本地观察的 `Selected`，不能声称 `Corroborated`。
- `TransactionSender` 复用 `eth_getTransactionByBlockHashAndIndex` 的 method-aware plan；sender 的输入 hash 绑定和 envelope 校验已接入，值与 null/NotFound 的 HTTP/WSS 证据及多源冲突/malformed 反例已锁定；跨 transport 负向 fixture 和 `SimulateV1` 默认构建导入边界仍开放。
- pending 的 source-local、quorum 与 Decision 语义仍需单独冻结；AtHash 不改变 pending、CallResult 或订阅边界。

建议的回归矩阵：

| 方法族 | 必须覆盖 | 当前结论 |
| --- | --- | --- |
| `*AtHash` | hash 对象编码、`requireCanonical=false`、显式 hash 绑定、缺块、冲突、跨 HTTP/WSS | root/view facade 已接入；跨 HTTP/WSS 事实待补 |
| `Pending*` 状态读取 | pending 本地观察、不同 source 的 pending 差异、空值、取消、材料和 source-local 诊断 | plan 已声明；公开结果和严格策略待决策 |
| `PendingCallContract` | revert/data presence、pending 参数、source-local 与 quorum 关系 | plan/typed/facade 均待补齐 |
| `PendingTransactionCount` | `eth_getBlockTransactionCountByNumber` catalog、quantity 解码、source-local 语义 | 已注册 PendingObserve plan；跨 transport 与 null/malformed 证据仍待补 |

相关验证命令：

```text
go test ./rpc/methods -run 'EIP1898|Pending' -count=1
go test ./rpc/methods -race -run 'EIP1898|Pending' -count=1
git diff --check
```

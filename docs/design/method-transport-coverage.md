# 方法与传输覆盖清单

状态：**M0 完成；M1/M2 状态 Hash、M3/M4 与 T0 代表 transport 切片完成；F0 前矩阵核对完成（2026-11-07）**。

F0 前逐项核对见 [`f0-method-matrix-review.md`](f0-method-matrix-review.md)。本文仍保留方法级实现与证据索引；Pending、订阅、同步发送和扩展方法的未承诺边界不能从“Registered”或普通 HTTP/WSS request 能力推导为已支持。

本清单以仓库锁定的 go-ethereum `v1.17.5`、`rpc/methods` catalog/ReadPlan、根 `Client` 和 `EthClient` 视图为事实源。它只记录当前代码事实和缺口，不授权单个方法任务修改公共接口。公共接口变更统一留给 roadmap 的 R0/F0。

## 覆盖分类

- **Root**：根 `Client` 是否有带 `CallResult` 的原生方法。
- **View**：`Client.EthClient()` 是否提供官方 go-ethereum 形态的方法。
- **RPC**：是否有项目 catalog/ReadPlan 或 write pipeline 的明确执行路径。
- **HTTP/WSS**：现有 transport 是否有请求/应答能力；“WSS request”不是订阅。
- **Subscription**：是否有订阅能力。当前没有生产订阅实现。
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
| `eth_getStorageAt` | Registered | `StorageAt` | `StorageAt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 空 bytes、EIP-1898 和跨 transport |
| `eth_getCode` | Registered | `CodeAt` | `CodeAt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | empty code、历史引用和跨 transport |
| `eth_call` | Registered | `CallContract` | `CallContract` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed`, call rules | overrides、provider golden `error.data=null` |
| `eth_estimateGas` | Registered | `EstimateGas` | `EstimateGas` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | provider revert golden、边界参数 |
| `eth_getLogs` | Registered | `FilterLogs` | `FilterLogs` | native HTTP/WSS raw call | 订阅未实现 | `eth.go`, `typed` | reorg、订阅契约和大范围成本 |
| `eth_feeHistory` | Registered | `FeeHistory` | `FeeHistory` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 历史高度、percentile 边界和跨 transport |
| `eth_gasPrice` | Registered | `SuggestGasPrice` | `SuggestGasPrice` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | selected 语义和异常来源 |
| `eth_maxPriorityFeePerGas` | Registered | `SuggestGasTipCap` | `SuggestGasTipCap` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | selected 语义和异常来源 |
| `eth_blobBaseFee` | Registered | `BlobBaseFee` | `BlobBaseFee` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | source 支持差异 |
| `eth_blockNumber` | Registered | `BlockNumber` | `BlockNumber` | native HTTP/WSS raw call | 不适用 | `eth.go`, `anchor.go` | reference 附带事实、跨 transport |
| `eth_createAccessList` | Registered | `CreateAccessList` | 不暴露 | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | extension 的官方生态边界 |
| `net_peerCount` | Registered | `PeerCount` | `PeerCount` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | source-local selected 语义 |
| `eth_syncing` | Registered | `SyncProgress` | `SyncProgress` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | syncing/null/对象差异 |
| `net_version` | Registered | `NetworkID` | `NetworkID` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | decimal identity 与 ChainID 关系 |
| `net_listening` | Registered | `Call` only | 不暴露 | raw dynamic read path | 不适用 | `call.go`, catalog | 没有 typed wrapper；需明确是否继续 Call-only |
| `eth_sendRawTransaction` | Write | `SendRawTransaction` | `SendTransaction` | native HTTP/WSS raw call | 不适用 | `write.go`, write pipeline | accepted/unknown/rejected 与 receipt 关系 |
| `eth_getProof` | Explicit unsupported | 不支持 | 不支持 | 不应 silent forward | 不适用 | catalog descriptor | 需要独立 proof admission/evidence contract |

项目 catalog 目前登记 23 个方法级条目，另有写入和显式 unsupported 条目。catalog 的注册不等于跨 transport、provider 或订阅证据已经完成。

## go-ethereum v1.17.5 官方 ethclient 方法

下面列出锁定版本 `ethclient.Client` 的公开业务方法。当前 EthClient 承诺方法契约冻结表中的 27 个 typed reads（含 HeaderBy* 与四个 `*AtHash`）、`SendTransaction` 和 `Close`；其余方法必须逐项决定为待接入、明确不支持或不属于当前 facade。

| 官方方法 | 当前 View | Root/项目映射 | 分类 |
| --- | --- | --- | --- |
| `Close()` | 已有 | `Client.Close` | 生命周期 |
| `ChainID` | 已有 | `Client.ChainID` | 已覆盖 |
| `BlockByHash` | 已有 | `Client.BlockByHash` | 已覆盖 |
| `BlockByNumber` | 已有 | `Client.BlockByNumber` | 已覆盖 |
| `BlockNumber` | 已有 | `Client.BlockNumber` | 已覆盖 |
| `PeerCount` | 已有 | `Client.PeerCount` | 已覆盖 |
| `BlockReceipts` | 已有 | `Client.BlockReceipts` | 已覆盖 |
| `HeaderByHash` | 已有（M2） | `Client.HeaderByHash` | 已接入；header-only/fullTx=false 与 BlockByHash 共用 plan |
| `HeaderByNumber` | 已有（M2） | `Client.HeaderByNumber` | 已接入；moving tags 复用 anchor，pending 本地拒绝 |
| `TransactionByHash` | 已有 | `Client.TransactionByHash` | 已覆盖 |
| `TransactionSender` | 未有 | 无 root typed 映射 | 待决定 |
| `TransactionCount` | 未有 | 无 root typed 映射 | 待决定 |
| `TransactionInBlock` | 未有 | 无 root typed 映射 | 待决定 |
| `TransactionReceipt` | 已有 | `Client.TransactionReceipt` | 已覆盖 |
| `SyncProgress` | 已有 | `Client.SyncProgress` | 已覆盖 |
| `NetworkID` | 已有 | `Client.NetworkID` | 已覆盖 |
| `BalanceAt` | 已有 | `Client.BalanceAt` | 已覆盖 |
| `BalanceAtHash` | 已有（M1/M2） | `Client.BalanceAtHash` | 已接入；EIP-1898 hash object |
| `StorageAt` | 已有 | `Client.StorageAt` | 已覆盖 |
| `StorageAtHash` | 已有（M1/M2） | `Client.StorageAtHash` | 已接入；EIP-1898 hash object |
| `CodeAt` | 已有 | `Client.CodeAt` | 已覆盖 |
| `CodeAtHash` | 已有（M1/M2） | `Client.CodeAtHash` | 已接入；EIP-1898 hash object |
| `NonceAt` | 已有 | `Client.NonceAt` | 已覆盖 |
| `NonceAtHash` | 已有（M1/M2） | `Client.NonceAtHash` | 已接入；EIP-1898 hash object |
| `FilterLogs` | 已有 | `Client.FilterLogs` | 已覆盖 |
| `SubscribeTransactionReceipts` | 未有 | 无订阅实现 | 订阅待设计 |
| `SubscribeNewHead` | 未有 | 无订阅实现 | 订阅待设计 |
| `SubscribeFilterLogs` | 未有 | 无订阅实现 | 订阅待设计 |
| `PendingBalanceAt` | 未有 | 无 root typed 映射 | 待决定 |
| `PendingStorageAt` | 未有 | 无 root typed 映射 | 待决定 |
| `PendingCodeAt` | 未有 | 无 root typed 映射 | 待决定 |
| `PendingNonceAt` | 未有 | 无 root typed 映射 | 待决定 |
| `PendingTransactionCount` | 未有 | 无 root typed 映射 | 待决定 |
| `CallContract` | 已有 | `Client.CallContract` | 已覆盖 |
| `CallContractAtHash` | 未有 | 无 root typed 映射 | 待决定 |
| `PendingCallContract` | 未有 | 无 root typed 映射 | 待决定 |
| `SuggestGasPrice` | 已有 | `Client.SuggestGasPrice` | 已覆盖 |
| `SuggestGasTipCap` | 已有 | `Client.SuggestGasTipCap` | 已覆盖 |
| `BlobBaseFee` | 已有 | `Client.BlobBaseFee` | 已覆盖 |
| `FeeHistory` | 已有 | `Client.FeeHistory` | 已覆盖 |
| `EstimateGas` | 已有 | `Client.EstimateGas` | 已覆盖 |
| `EstimateGasAtBlock` | 未有 | 无 root typed 映射 | 待决定 |
| `EstimateGasAtBlockHash` | 未有 | 无 root typed 映射 | 待决定 |
| `SendTransaction` | 已有 | `Client.SendRawTransaction` / view SendTransaction | 已覆盖 |
| `SendTransactionSync` | 未有 | 无 root typed 映射 | 待决定 |
| `SendRawTransactionSync` | 未有 | 无 root typed 映射 | 待决定 |
| `SimulateV1` | 未有 | 无 root typed 映射 | 待决定 |

`go-ethereum/ethclient/gethclient` 还提供扩展方法，例如 `GetProof`、`CallContract` overrides、`Trace*`、`SubscribePendingTransactions` 和调试统计。它们不自动进入 EthClient；必须单独决定是否属于项目的生态承诺。

## 当前 transport 事实

| 能力 | native HTTP | native WSS | gethcompat | 备注 |
| --- | --- | --- | --- | --- |
| 普通 JSON-RPC request/response | 已有 | 已有 | 显式 adapter | 共用 `pool.Transport` seam |
| request ID demux | HTTP 请求级 | 已有 | 由 geth | WSS raw-call 已测试乱序和 pending 清理 |
| `ChainID` / `Close` | 已有 | 已有 | 显式 adapter | 共享 owner 生命周期 |
| `error.data` presence | 已有四态 | native raw 可保留；provider golden 未闭合 | 受 adapter 限制 | missing/null/value/unknown 必须分开记录 |
| 普通订阅 | 不适用 | 未实现 | 未承诺 | Transport Port 不包含 Subscribe |
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

订阅实现还必须增加取消、Unsubscribe、backpressure、reorg、重复事件、缺口、source snapshot 和 goroutine 泄漏测试。

## M1/M2 状态 Hash 切片结果

- `BalanceAtHash`、`NonceAtHash`、`StorageAtHash`、`CodeAtHash` 复用各自既有 ReadPlan、typed projection、请求冻结和执行入口；最后一个参数在 wire 上固定为 `{blockHash, requireCanonical:false}`。
- 根 Client 返回 `(T, CallResult, error)`，EthClient 视图返回 go-ethereum v1.17.5 的 `(T, error)`；四个 view 方法只投影同一 owner execution。
- 显式 hash 不建立 moving reference，也不走未知 `Call`；上游缺块/RPC 错误、冲突、解码错误和取消沿既有 Decision/错误/材料/Close 规则返回。

## M2 区块首个切片结果

- `HeaderByHash` 和 `HeaderByNumber` 复用 `eth_getBlockByHash` / `eth_getBlockByNumber` 的既有 ReadPlan、admission 和 block projector；只把 `fullTx` 固定为 `false`，再由 `DecodeBlockHeaderOnly` 交付 `*types.Header`。
- 根 Client 返回 `(* types.Header, CallResult, error)`，EthClient 视图保持官方 `(* types.Header, error)`。
- `HeaderByNumber(nil)`、`-2/-3/-4` 通过已建立 reference 读取并附带 reference 事实；`-5` 映射高度 `0x0`；`-1` 及未知负数在本地拒绝，不宣称 pending header 已支持。
- 定向测试：`typed_block_headers_test.go`、`typed_state_hash_test.go`、`ethclient_view_test.go` 方法集合 29 项（27 个 typed reads、`SendTransaction`、`Close`）。

## 未决项

- 官方 pending 族、交易派生方法是否进入 EthClient；
- `eth_getProof`、`SimulateV1`、gethclient 扩展是否属于项目承诺；
- root 订阅事件类型与官方视图订阅 channel 的关系；
- subscription 的 quorum、source membership、reorg/removed、Pull 补查和异步错误；
- 方法覆盖完成后，是否需要调整 `CallResult`、ResultCapture 或引入独立事件结果类型。

这些问题由 roadmap 的 R0/F0 统一收敛，不由单个方法 agent 局部决定。

## M1/M3 前置复核：state hash 与 pending

本轮未接入 pending 方法；复核结论如下：

- `BalanceAtHash`、`StorageAtHash`、`CodeAtHash`、`NonceAtHash` 在 go-ethereum v1.17.5 中仍调用现有四个 RPC 方法，只是把最后一个 block 参数编码成 EIP-1898 的 `{blockHash, requireCanonical:false}` 对象。
- 当前 `rpc/methods` 的 `ReferenceSpec{EIP1898:true}` 和 EIP-1898 rewrite 测试继续覆盖对象校验、显式 hash、`requireCanonical` 保留和 moving tag 改写；AtHash facade 通过同一 plan 执行。
- AtHash 接入使用受控的 EIP-1898 map 参数和现有 plan，不通过未知 `Call`。
- `PendingBalanceAt`、`PendingStorageAt`、`PendingCodeAt`、`PendingNonceAt` 的 plan 已有 `PendingObserve` 声明，但当前公开 facade 没有对应方法。pending 是节点本地状态，不能直接套用 moving reference；是否允许多源严格比较、是否接受单源本地观察、以及最终 Decision 状态必须由 R0 冻结。
- `PendingTransactionCount`、`PendingCallContract` 还缺少对应的公开 plan/typed 交付闭环；不能只增加一个薄 wrapper。
- 因此 M1/M3 的下一步准入仅剩 pending：先冻结其 source-local、quorum 与 Decision 语义，再决定是否扩展 facade；本轮 AtHash 不改变 pending/CallResult/订阅边界。

建议的回归矩阵：

| 方法族 | 必须覆盖 | 当前结论 |
| --- | --- | --- |
| `*AtHash` | hash 对象编码、`requireCanonical=false`、显式 hash 绑定、缺块、冲突、跨 HTTP/WSS | root/view facade 已接入；跨 HTTP/WSS 事实待 T0 |
| `Pending*` 状态读取 | pending 本地观察、不同 source 的 pending 差异、空值、取消、材料和 source-local 诊断 | 计划声明存在；公开结果和严格策略待 R0 |
| `PendingCallContract` | revert/data presence、pending 参数、source-local 与 quorum 关系 | plan/typed/facade 均待补齐 |
| `PendingTransactionCount` | `eth_getBlockTransactionCountByNumber` catalog、quantity 解码、source-local 语义 | 当前只有官方 ethclient 参考，无项目执行路径 |

验证命令（本轮只读）：

```text
go test ./rpc/methods -run 'EIP1898|Pending' -count=1
go test ./rpc/methods -race -run 'EIP1898|Pending' -count=1
git diff --check
```

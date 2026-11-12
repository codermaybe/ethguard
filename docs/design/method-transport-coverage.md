# 方法与传输覆盖清单

状态：**当前 v1.17.10 完整兼容矩阵（2026-11-12）；尚未达到完整 ethclient 发布门槛**。

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
| `eth_getTransactionReceipt` | Registered | `TransactionReceipt` | `TransactionReceipt` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed`, `rpc/internal/objects/receipt` | 决策 0068；`TestReceiptWireOfficialVectors`／`TestReceiptWireCapturedReplay` 独立 shape oracle＋typed 保真，`TestReceiptWireRequiredFaultKeepsFrozenDenominator`／`TestReceiptWireDoesNotMergeDistinctClaims` 覆盖故障与等价边界；无新增 provider 观察 |
| `eth_getTransactionByHash` | Registered | `TransactionByHash` | `TransactionByHash` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | pending 与 absent 的跨 transport 矩阵 |
| `eth_getBlockReceipts` | Registered | `BlockReceipts` | `BlockReceipts` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed`, receipt objects | 与单 receipt 共用官方形态／保护合同；`TestReceiptWireDoesNotMergeDistinctClaims` 验证 nullable presence／DATA 异见；大数组、逐 receipt 绑定成本和真实 provider 非空批次证据仍待补 |
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
| `eth_syncing` | Registered | `SyncProgress` | `SyncProgress` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 七个上游字段已投影；`TestSyncProgressTransportProjectionAndDone` 覆盖 Root／EthClient、HTTP／raw WSS、三类 remaining counter；真实 provider 对象形态仍未观察 |
| `net_version` | Registered | `NetworkID` | `NetworkID` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | decimal identity 与 ChainID 关系 |
| `net_listening` | Registered | `Call` only | 不暴露 | raw dynamic read path | 不适用 | `call.go`, catalog | 没有 typed wrapper；需明确是否继续 Call-only |
| `eth_sendRawTransaction` | Write | `SendRawTransaction` | `SendTransaction` | native HTTP/WSS raw call | 不适用 | `write.go`, write pipeline | accepted/unknown/rejected 与 receipt 关系 |
| `eth_getProof` | Explicit unsupported | 不支持 | 不支持 | 不应 silent forward | 不适用 | catalog descriptor | 需要独立 proof admission/evidence contract |

两个计数口径不同，不能互相换算：**47** 是锁定上游 ethclient 的用户可调用方法面（含 Close，排除 `Client()`），**26** 是本项目 catalog 登记的注册读 RPC 条目——一个 RPC 可服务普通／AtHash／Pending 多个公开入口，另有写入条目与显式 unsupported 条目及 root 扩展在 47 的分母内但不占 26 的行。catalog 的注册不等于跨 transport、provider 或订阅证据已经完成。

## 读注册归属（rpc/methods）

全部 26 个注册读方法已自治：每个方法文件拥有自己的 RPC 名称、公开映射、MethodSpec／只读 MethodDescriptor、准备与修正 hooks；`spec_inventory.go` 与 `catalog.go` 只收集定义函数，集中名称表已退役（盘点基线源码 `04f1f05`，门禁与自检见下文记录）。`eth_sendRawTransaction`（WriteMethod）与 `eth_getProof`（UnsupportedMethod）无读 spec。无遗留 `XxxPolicy()` 构造器；`blockPolicy` 是 block 包装 resolver 的装配。

| RPC 方法 | Owner 文件（rpc/methods） | 执行类别 | 公开入口（共享同一 plan） |
| --- | --- | --- | --- |
| `eth_getTransactionByHash` | `transaction_by_hash.go` | strict 事务对象 | `TransactionByHash` |
| `eth_getTransactionByBlockHashAndIndex` | `transaction_in_block.go` | strict 事务对象 | `TransactionInBlock`、`TransactionSender` |
| `eth_chainId` | `chain_id.go` | strict quantity（canonical bytes） | `ChainID` |
| `eth_blockNumber` | `block_number.go` | median quantity | `BlockNumber` |
| `eth_gasPrice` | `gas_price.go` | median quantity | `SuggestGasPrice` |
| `eth_maxPriorityFeePerGas` | `max_priority_fee_per_gas.go` | median quantity | `SuggestGasTipCap` |
| `eth_blobBaseFee` | `blob_base_fee.go` | median quantity | `BlobBaseFee` |
| `net_peerCount` | `net_peer_count.go` | local folded quantity | `PeerCount` |
| `net_version` | `net_version.go` | strict decimal identity | `NetworkID` |
| `net_listening` | `net_listening.go` | local object（root 扩展） | Call only |
| `eth_getBalance` | `get_balance.go` | strict quantity + hashAt reference + pending | `BalanceAt`、`BalanceAtHash`、`PendingBalanceAt` |
| `eth_getTransactionCount` | `get_transaction_count.go` | strict quantity + hashAt reference + pending | `NonceAt`、`NonceAtHash`、`PendingNonceAt` |
| `eth_getBlockTransactionCountByHash` | `block_transaction_count_by_hash.go` | strict quantity（null 准入） | `TransactionCount` |
| `eth_getBlockTransactionCountByNumber` | `block_transaction_count_by_number.go` | strict quantity + heightAt reference + pending | `PendingTransactionCount` |
| `eth_getStorageAt` | `get_storage_at.go` | strict storage word + hashAt reference + pending | `StorageAt`、`StorageAtHash`、`PendingStorageAt` |
| `eth_getCode` | `get_code.go` | strict data + hashAt reference + pending | `CodeAt`、`CodeAtHash`、`PendingCodeAt` |
| `eth_getTransactionReceipt` | `transaction_receipt.go` | strict receipt 对象 | `TransactionReceipt` |
| `eth_getBlockReceipts` | `get_block_receipts.go` | strict receipt 列表 + hashAt reference、拒绝 pending | `BlockReceipts` |
| `eth_getLogs` | `get_logs.go` | strict log 列表 + heightFields reference、拒绝 pending | `FilterLogs`、`SubscribeFilterLogs`（同源 Pull） |
| `eth_syncing` | `syncing.go` | local 对象（canonical JSON） | `SyncProgress` |
| `eth_feeHistory` | `fee_history.go` | strict feeHistory 对象 + heightAt reference + pending | `FeeHistory` |
| `eth_createAccessList` | `create_access_list.go` | strict accessList 对象 + hashAt reference + pending | `CreateAccessList`（非 ethclient 表面） |
| `eth_getBlockByHash` | `get_block_by_hash.go` | block 包装 strict | `BlockByHash`、`HeaderByHash` |
| `eth_getBlockByNumber` | `get_block_by_number.go` | block 包装 strict；moving tag anchor | `BlockByNumber`、`HeaderByNumber` |
| `eth_call` | `call.go` | call-revert 受控逃逸 + hashAt reference + pending | `CallContract`、`CallContractAtHash`、`PendingCallContract` |
| `eth_estimateGas` | `estimate_gas.go` | median quantity + hashAt reference + pending | `EstimateGas`、`EstimateGasAtBlock`、`EstimateGasAtBlockHash` |
| `eth_sendRawTransaction` | `send_raw_transaction.go` | WriteMethod；写入管线，无读 spec | `SendTransaction`、`SendRawTransaction`、`SendTransactionSync`、`SendRawTransactionSync` |
| `eth_getProof` | `get_proof.go` | UnsupportedMethod；显式缺口，不得静默转发 | 无 |
| 订阅族（newHeads／logs／receipts） | 无读注册条目 | stream 族，独立事件模型与订阅合同 | `SubscribeNewHead`、`SubscribeFilterLogs`、`SubscribeTransactionReceipts` |
| 派生族（sender、header 投影） | 无 RPC 条目 | derivation 族，本地推导，不注册 RPC policy | `TransactionSender`、header 视图 |

无特殊修正规则是多数方法的默认裁决：全部标量方法直接复用 wire codec，不新增修正 switch；对象方法的修正只在各自 owner 文件按族裁决并记录。

对象族内嵌交易结论（2026-11-12）：`objects/block` 的 `encodeTransactions` 对完整内嵌交易只做 go-ethereum `types.Transaction` 往返与块级绑定，不复制独立 query 的官方 schema 分支解码、pending 派生或生成器补丁；内嵌交易必有块上下文，其保护域由块 admission（uncle root、头绑定）覆盖。两套语义各自明确，无需接入；若未来需要统一另交独立提交。

迁移期间门禁记录（2026-11-12，对应各迁移批次完成时点）：`gofmt -l`、`go vet ./...`、`go test ./... -count=1`、`go test ./... -race -count=1`、`go test . -run 'TestArchitecture|TestPackage|TestDefaultBuild'`、`go test ./internal/verification/integration -run '^TestVerification' -race -count=2 -shuffle=on`、Anvil 事务生命周期（`-tags=anvil`，固定 1.7.1，88 条断言）在迁移完成时全部通过。未新增公网 provider 观察；离线与本地 Anvil 证据按上文边界记录。

局部热路径测量（2026-11-12，`BenchmarkTransactionByHashNormalize`／`BenchmarkTransactionByHashAdmit`，`-benchtime=1s`，单个 ~1.3KB pending 官方向量）：规范键准备约 217µs／响应（59.7KB 已分配、1176 allocs），准入（准备 + 对象校验）约 424µs（109.7KB 已分配、2123 allocs）。这是方法准备与准入两步的局部测量，不是完整生产 assembler 路径，也未含任何提供方网络往返；不据此下网络数量级或全链路主要成本结论。typed oneOf 分支顺序试探在该向量上耗时显著，如后续完整 profile 显示需要，另行单独优化提交。

自检结论（2026-11-12，迁移完成后）：方法文件对其他方法文件私有函数的引用为零；共享依赖仅族 wrapper（block.go）、objects 层与 wire codec 三类。迁移逐字搬运，唯一一次简化尝试（官方闭对象的运行时拒绝）被既有 receipt vendor-dissent 验收否决并回退，未留下行为变更；无新接口、注册框架或 helper 包，inventory 为纯收集。

## 26 个读 RPC 的单源处理处置（2026-11-13）

本轮处理接口（canonical strict 单源结果）的逐族裁决。口径：**新接口接入** = 生产 invocation 冻结 canonical 路线；**对象内优化** = 不接 seam 但消除可证明重复；**保留合同** = 按现有执行合同运行并记录理由；**明确缺口** = 记录未闭合项。数量：接入 2、保留 24（其中 6 个记录了可评估的接入/共享路径）、缺口 0（SimulateV1 缺口属于 47 方法面，见下文方法差异基线，不在 26 读 RPC 之列）。处置证据来自 T00 事实快照、T02/T07/T08 见证与逐族代码核查。

| 批次 | 读取 RPC | 处置 | 理由与证据 |
| --- | --- | --- | --- |
| T09a | `eth_getTransactionByHash`、`eth_getTransactionByBlockHashAndIndex` | 新接口接入 | T05–T07 试点完成并复核：冻结路线、一次 processor、零 raw-hook 重跑、Fault/健康等价、差分与计数见证（`aggregator/canonical_route_test.go`、facade `transaction_canonical_route_test.go`）。KeySource 已收敛 canonical-bytes，分簇等价有见证（`TestTransactionKeySourceSwitchKeepsClusters`）。 |
| T09b | `eth_getTransactionReceipt`、`eth_getBlockReceipts` | 保留合同（可接入路径已记录） | 与试点同构：admission 已拥有准备类工作——`AdmitTransaction→validateAndDecode` 内部完整执行一次 `receiptobject.Encode`（prune+typed+merge）再解码绑定 TxHash，集群 Encode 再跑一次完整 Encode，即每来源两次完整编码，正是试点消除的形状。未接入的原因是等价性负担而非结构障碍：nullable/DATA presence 决策 0068、空数组与 null 语义（`AdmitBlock`）、逐 receipt 请求绑定与大数组成本须各有差分见证（receipt vendor-dissent 曾否决过一次简化尝试）。接入路径：receipt 对象按 transaction 同法建立 Candidate 内核 → 两方法 processor → KeySource 已是 canonical-bytes 无需切换。 |
| T09c | `eth_getLogs`、`eth_feeHistory`、`eth_createAccessList` | 保留合同 | logs 的 admission 只做 requiredness/请求绑定校验（`logobject.ValidateResponse`），不重复 Encode 的解析——无试点要消除的处理重复；feeHistory admission 的形状校验+range 校验与 Encode 解析部分重叠但非同一完整处理；accessList 无 Admit。三者各有独立接受域（范围/ratio/request validation/pending refuse/observe），迁移须逐族证明 Fault 阶段等价且收益未证实。另记录一处对象内重复：`admitLogsResponse` 与 `logobject.ValidateResponse` 对 null/array 的前置检查重复一次轻量遍历，收益微小，保留原状。 |
| T09d | `eth_chainId`、`net_version`、`eth_getBalance`、`eth_getTransactionCount`、`eth_getStorageAt`、`eth_getCode`、`eth_getBlockTransactionCountByHash`、`eth_getBlockTransactionCountByNumber` | 保留合同 | 八个方法均无 Admit：每个来源在集群 Encode 中恰好一次准备，无重复处理可消除。无 Admit 的方法接入新 seam 会把 Encode 错误从 resolver 的 encode-skip（Fault 表内、不缩分母）改写为 admission 归因——设计禁止这种未证明的 Fault 阶段统一。KeySource 核查：7 个已是 canonical-bytes；`eth_getBlockTransactionCountByHash` 的 normalizer 键（十进制数量语法）与 encoder 输出（0x 十六进制）确实不同——窄键声明是真实的，不是遗留。pending-local 方法（balance/count/storage/code 的 PendingObserve）按路线冻结规则天然走完整原路线。 |
| T09e | `eth_getBlockByHash`、`eth_getBlockByNumber` | 保留合同 | 窄键裁决（number\|hash）与 canonical 字节键语义不同，不使用本 seam；full/header 保护域、uncle root 与头绑定是独立差分面。对象内重复评估结论：fullTx 响应的 admission（`AdmitBlock`）与集群 encode 都做交易 payload 往返，存在可共享空间；块对象 Candidate 化需独立的差分电池（内嵌交易已按 P5 结论独立评估，不复制独立 query 规则），本轮不接入。 |
| T09f | `eth_blockNumber`、`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_estimateGas` | 保留合同 | Median/Selected 采样独立：无聚类即无 canonical-bytes 聚类键，迁移会把采样语义升级为 Strict 裁决——设计明确禁止。核查无内部重复：median 每答案一次 normalize，estimateGas 无 Admit。异常源、tie、pending 语义保持。 |
| T09g | `net_peerCount`、`eth_syncing`、`net_listening` | 保留合同 | Local/Selected 独立，source-local 语义与 SyncProgress 全字段/Done()、bool/数量/null 拒绝规则不迁移。无 Admit、无重复处理。 |
| T09h | `eth_call` | 保留合同 | call-revert 受控逃逸：execution/data 两层裁决、revert 四态、受保护 data 与 provider 文案分离——canonical 结果类型有意不表达该维度（设计第 4 节固定接口明确排除 call-revert）。不把错误信封当普通 canonical Value。 |

补充口径：root-only `net_listening` 保留在 T09g 内（它在 26 读 RPC 的分母里，也显式记录为 root 扩展）；`eth_sendRawTransaction`（写入管线）与 `eth_getProof`（显式 unsupported）无读 spec，不参与本处置；订阅族消费同源 Pull（logs/receipts 的既有合同），本表不重复裁决。

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
| `SyncProgress` | 已有 | `Client.SyncProgress` | 七个上游同步／索引字段已补齐；公开双 facade 的 HTTP／raw WSS 验证 Done() 不因计数丢失过早成立；可选缺失为零与真实样本边界见兼容政策 |
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

# 方法与传输覆盖清单

状态：2026-11-17 文档基线。go-ethereum v1.17.10 的47项目标方法名称集合与公开业务类型迁移已完成；27个注册读 RPC。入口存在、类型生成和测试存在均不等于逐方法语义、真实 provider 或稳定发布验收完成。

## 1. 计数与类型口径

上游 ethclient.Client 有48个导出 receiver 方法，Client() 底层访问器明确排除，剩余47项含 Close。`scripts/ethclient-method-diff.sh` 检查名称集合，TestEthClientBusinessSignatures 按项目生成类型合同检查完整签名。27是注册读RPC数，一个RPC可以服务多个普通／AtHash／Pending入口；写入、订阅、派生和root扩展分别记账。

公开业务模型统一为 ethapi，写入使用 BinaryTransaction。view 有对应业务方法形态但不承诺 geth Go 名义类型身份。Root 返回业务值、CallResult、error；view省略CallResult。NewHead、Logs和Receipts在两入口均使用显式policy及Next/Basis/Close交付路径与证据，内容流另有CommittedTip。Logs新接口已于2026-11-24本地验收，Receipts已于2026-11-25本地验收；不保留channel和ethereum.Subscription桥接。CreateAccessList的any请求仍是明确扩展，不是未完成的geth类型迁移。

## 2. 覆盖分类与证据状态

Root／View记录入口，RPC记录plan或写入执行路径，HTTP/WSS记录request能力，Subscription记录独立流式能力。Evidence分别覆盖值、裁定、错误、reference、材料与生命周期，不能由Registered推出。

多源订阅使用固定WSS来源snapshot与strict quorum。NewHead每源一个当前Tip，沿验证后的父链计票，按hash补齐连续路径与完整重组，路径证人与对象供应独立记录；Logs和Receipts复用私有Head并按hash取得完整块/收据证明，Receipts另对实际交付的规范官方字段作固定Q裁定。内容流不依赖原生logs/transactionReceipts推送。NewHead的SourceSession是订阅会话代次，不是TCP连接ID。没有独立Selected订阅模式，multi-source Client只有一个WSS来源时拒绝，显式单源Client保持Forwarded。NewHead已于2026-11-23验收，Logs已于2026-11-24验收；Receipts已于2026-11-25验收，其余S1待办见订阅合同。

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
| `eth_getLogs` | Registered | `FilterLogs` | `FilterLogs` | native HTTP/WSS raw call | Logs订阅已改为Head+完整块承诺，不消费eth_getLogs子集 | `eth.go`, `typed`, `logs_subscription.go` | unary方法已验收；Logs连续流已验收，跨transport/provider及大范围成本独立 |
| `eth_simulateV1` | Registered | `SimulateV1` | `SimulateV1` | native HTTP/WSS raw call | 不适用 | `simulate.go`, `rpc/methods/simulate.go` | 生成 payload/base；Prepare 统一校验及固定输入。逐方法模拟语义与 provider 证据继续验收。完整 base/reference 矩阵见 [moving-tag-matrix](moving-tag-matrix.md#c-execution-ranges-fee-history) |
| `eth_feeHistory` | Registered | `FeeHistory` | `FeeHistory` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 历史高度、percentile 边界和跨 transport |
| `eth_gasPrice` | Registered | `SuggestGasPrice` | `SuggestGasPrice` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | selected 语义和异常来源 |
| `eth_maxPriorityFeePerGas` | Registered | `SuggestGasTipCap` | `SuggestGasTipCap` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | selected 语义和异常来源 |
| `eth_blobBaseFee` | Registered | `BlobBaseFee` | `BlobBaseFee` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | source 支持差异 |
| `eth_blockNumber` | Registered | `BlockNumber` | `BlockNumber` | native HTTP/WSS raw call | 不适用 | `eth.go`, `anchor.go` | reference 附带事实、跨 transport |
| `eth_createAccessList` | Registered | `CreateAccessList` | 不暴露 | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | extension 的官方生态边界 |
| `net_peerCount` | Registered | `PeerCount` | `PeerCount` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | source-local selected 语义 |
| `eth_syncing` | Registered | `SyncProgress` | `SyncProgress` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | 官方三个字段已迁生成模型；`TestSyncProgressTransportProjectionAndDone` 覆盖 Root／EthClient、HTTP／raw WSS、完整来源证据及三类 remaining counter 的独立检查；真实 provider 对象形态仍未观察 |
| `net_version` | Registered | `NetworkID` | `NetworkID` | native HTTP/WSS raw call | 不适用 | `eth.go`, `typed` | decimal identity 与 ChainID 关系 |
| `net_listening` | Registered | `Call` only | 不暴露 | raw dynamic read path | 不适用 | `call.go`, catalog | 没有 typed wrapper；需明确是否继续 Call-only |
| `eth_sendRawTransaction` | Write | `SendRawTransaction` | `SendTransaction` | native HTTP/WSS raw call | 不适用 | `write.go`, write pipeline | accepted/unknown/rejected 与 receipt 关系 |
| `eth_getProof` | Explicit unsupported | 不支持 | 不支持 | 不应 silent forward | 不适用 | catalog descriptor | 需要独立 proof admission/evidence contract |

两个计数口径不同，不能互相换算：**47** 是锁定上游 ethclient 的用户可调用方法面（含 Close，排除 `Client()`），**27** 是本项目 catalog 当前登记的注册读 RPC 条目——一个 RPC 可服务普通／AtHash／Pending 多个公开入口，另有写入条目与显式 unsupported 条目及 root 扩展在 47 的分母内但不占 27 的行。catalog 的注册不等于跨 transport、provider 或订阅证据已经完成。

## 读注册归属（rpc/methods）

27 个注册读方法各由自己的方法文件拥有 RPC 名称与执行定义；SimulateV1 已纳入同一库存。`spec_inventory.go` 与 `catalog.go` 只收集定义函数。`eth_sendRawTransaction`（WriteMethod）与 `eth_getProof`（UnsupportedMethod）无读 spec。无遗留 `XxxPolicy()` 构造器；`blockPolicy` 是 block 包装 resolver 的装配。

| RPC 方法 | Owner 文件（rpc/methods） | 执行类别 | 公开入口（共享同一 plan） |
| --- | --- | --- | --- |
| `eth_simulateV1` | `simulate.go` | strict + reference / pending-local | `Client.SimulateV1`、`EthClient.SimulateV1`、Raw Call |
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
| `eth_getLogs` | `get_logs.go` | strict log 列表 + heightFields reference、拒绝 pending | `FilterLogs`；Logs连续订阅不再走eth_getLogs Pull |
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

## 3. 单源准备与内部收敛状态

| 路径 | 当前事实 | 后续边界 |
| --- | --- | --- |
| 两个独立transaction查询 | 已接入canonical processor，Candidate共享表示、检查与绑定；raw适配保留 | pending／mined、类型与方言证据继续按方法验收 |
| 单收据 | 873c5b8接入processor，复用DecodeWithCanonical已检查字节；真实Aggregator、错误／健康／材料与分配收益已验收 | 仅strict单收据；不扩张为大日志、高来源数或provider性能结论 |
| BlockReceipts | raw AdmitBlock与EncodeBlock保留，共用receipt保护内核 | null／空数组／逐条绑定／大批次成本独立审查，不自动复制单收据结论 |
| FeeHistory／AccessList／共享Log | 已定位内部手写平行定义与接受域差异 | 先完成本文档系列并保存Git，再按方法合同确定目标并实施 |
| 完整Block／Header／Transaction／Receipt | 已有生成模型接入及算法／绑定检查 | 检查剩余重复而非重新迁移；Block仍为ResolverBlock，不能套Strict processor |
| 标量、Median、Local、CallRevert | 保留各自比较或选择语义、错误阶段及保证范围 | 不以接入processor数量衡量完成，不在文档整理中统一错误归因 |

公开生成模型与内部对象内核是两个验证面。共享Log仍有手写CanonicalLog；完整block admission当前使用transaction Candidate／私有geth处理。旧“Log全路径已生成化”“内嵌交易只做geth往返”等阶段描述不再作为现行事实。

## go-ethereum v1.17.10 官方 ethclient 方法

2026-11-25按锁定模块源码重新核对：`scripts/ethclient-method-diff.sh`实际输出上游48项、本项目view47项、业务缺失0；`TestEthClientMethodSetIsExact`与`TestEthClientBusinessSignatures`本轮重新通过。该检查覆盖receiver方法名称和项目声明的完整签名，并不包含上游包级函数，也不意味着所有参数选择与使用方式相同。

| 用户可调用选择 | 本仓库表面及限制 |
| --- | --- |
| 47项业务receiver方法（含Close） | 全部有入口；普通/AtHash/Pending、gas估算、同步写入、Simulate与三种订阅均在集合内 |
| 上游`Client()`与`NewClient(*rpc.Client)` | 不提供底层geth RPC访问器，也不接管任意geth连接；本仓库使用`New(config, opts...)`与`Client.EthClient()` |
| 包级`Dial`、`DialContext` | 有对应入口但合同不同：本仓库惰性构造，至少两个不同端点；单源显式`DialUncorroborated`；配置URL支持HTTP(S)/WS(S)，不包含IPC路径 |
| geth名义类型及bind接口 | 业务类型采用`ethapi`，不直接满足geth的`bind/v2`接口；需要调用方适配 |
| 数字高度、hash、latest/safe/finalized/earliest | 有对应入口和selector；moving tag按本项目reference策略建立证据，finalized preset的default/latest采用保守finalized算法；不能按上游单源latest保证解释 |
| pending选择 | 状态读取、call、交易数等提供pending-local路径；typed Block/Header、BlockReceipts及FilterLogs的pending范围明确本地拒绝。入口名称齐全不表示接受域一致 |
| 三种订阅 | policy＋Next/Basis/Close（内容流另有CommittedTip），完整连续批次与审计；没有上游channel/ethereum.Subscription方式，出生前不补历史 |
| 同步发送 | 对应方法存在，项目执行一次广播＋客户端receipt观察；上游调用服务端`eth_sendRawTransactionSync`。期限与证据按项目同步写入合同，不能认为执行机制等价 |
| Simulate选项 | 生成模型具备blockStateCalls、traceTransfers、validation、returnFullTransactions及base selector；完整模拟行为及真实provider证据仍独立 |
| 包级`RevertErrorData` | 本仓库没有同名helper；`RevertError`有Data/DataRaw/ErrorData，但没有ErrorCode。将其交给锁定上游helper得到`nil,false`，不能直接互用；调用方可`errors.As`取得项目错误后读取数据 |
| gethclient扩展 | GetProof、overrides/Trace、pending交易订阅等不属于上述47项；`eth_getProof`明确unsupported，未知公开Call不能补齐它们 |

Revert helper差异已用实际Go程序核对：``&ethguard.RevertError{Data: []byte{0xde,0xad}, DataRaw: json.RawMessage(`"0xdead"`)}``的`ErrorData()`为`0xdead`，上游`ethclient.RevertErrorData`却返回`nil,false`，因为还要求`rpc.Error.ErrorCode()==3`。现有DataError测试不证明该包级helper兼容；这是当前使用方式差异，不据此静默增加JSON-RPC错误码或改变聚合错误语义。

下面列出锁定版本 `ethclient.Client` 的公开方法。当前目标方法集合为 47 项；上游
`Client()` 只作为显式排除项记录。方法名出现在 facade 后，仍须逐项闭合语义证据；
入口接入或计数完整不能将“待验收”当作已完成。

| 官方方法 | 当前 View | Root/项目映射 | 分类 |
| --- | --- | --- | --- |
| `Close()` | 已有 | `Client.Close` | 生命周期 |
| `Client()` | 明确排除 | 无映射 | 上游基础设施访问器，不属于业务兼容面 |
| `ChainID` | 已有 | `Client.ChainID` | 入口已接入；逐方法证据仍按本清单验收 |
| `BlockByHash` | 已有 | `Client.BlockByHash` | 入口已接入；逐方法证据仍按本清单验收 |
| `BlockByNumber` | 已有 | `Client.BlockByNumber` | 入口已接入；逐方法证据仍按本清单验收 |
| `BlockNumber` | 已有 | `Client.BlockNumber` | 入口已接入；逐方法证据仍按本清单验收 |
| `PeerCount` | 已有 | `Client.PeerCount` | 入口已接入；逐方法证据仍按本清单验收 |
| `BlockReceipts` | 已有 | `Client.BlockReceipts` | 入口已接入；逐方法证据仍按本清单验收 |
| `HeaderByHash` | 已验收 | `Client.HeaderByHash` | 2026-11-23保留实现；header-only/fullTx=false、请求hash绑定与共享官方Header内核核对，定向普通/race通过；不证明完整body |
| `HeaderByNumber` | 已验收 | `Client.HeaderByNumber` | 2026-11-23显式高度/pending及默认preset的nil/latest/safe/finalized→hash pin验收；新增root/view的reference失败零业务请求、业务失败留Reference与capture反例，普通/race通过；finalized preset例外归reference合同 |
| `TransactionByHash` | 已有 | `Client.TransactionByHash` | 入口已接入；逐方法证据仍按本清单验收 |
| `TransactionSender` | 已验收 | `Client.TransactionSender` | 2026-11-23输入/hash/block/index、null/错误及声明sender合同验收；`TestTransactionSenderFromOnlyConflictAcrossTransports`和`TestTransactionSenderDeliversReportedSenderAcrossTransports`覆盖仅from异值/共同声明、HTTP+raw WSS、root/view及capture；11项定向普通/race通过，未新增真实provider证据 |
| `TransactionCount` | 已有 | `Client.TransactionCount` | 值/null/Conflict/Malformed 已有 facade 断言（`TestTransactionCountMultiSourceConflictKeepsEvidence`、`TestTransactionCountMultiSourceMalformedFailsWithoutValue`）；跨 transport 负向 fixture 仍待补 |
| `TransactionInBlock` | 已有 | `Client.TransactionInBlock` | 值/null/Conflict/Malformed 已有 facade 断言（`TestTransactionInBlockMultiSourceConflictKeepsEvidence`、`TestTransactionInBlockMultiSourceMalformedFailsWithFaults`）；inclusion envelope 跨 transport 负向 fixture 仍待补 |
| `TransactionReceipt` | 已有 | `Client.TransactionReceipt` | 入口已接入；逐方法证据仍按本清单验收 |
| `SyncProgress` | 已有 | `Client.SyncProgress` | 返回官方生成模型；公开双 facade 的 HTTP／raw WSS 验证三个 optional 字段及原始来源计数保真；不提供 geth Done()，真实样本边界见兼容政策 |
| `NetworkID` | 已有 | `Client.NetworkID` | 入口已接入；逐方法证据仍按本清单验收 |
| `BalanceAt` | 已有 | `Client.BalanceAt` | 入口已接入；逐方法证据仍按本清单验收 |
| `BalanceAtHash` | 已有 | `Client.BalanceAtHash` | 已接入；EIP-1898 hash object |
| `StorageAt` | 已有 | `Client.StorageAt` | 入口已接入；逐方法证据仍按本清单验收 |
| `StorageAtHash` | 已有 | `Client.StorageAtHash` | 已接入；EIP-1898 hash object |
| `CodeAt` | 已有 | `Client.CodeAt` | 入口已接入；逐方法证据仍按本清单验收 |
| `CodeAtHash` | 已有 | `Client.CodeAtHash` | 已接入；EIP-1898 hash object |
| `NonceAt` | 已有 | `Client.NonceAt` | 入口已接入；逐方法证据仍按本清单验收 |
| `NonceAtHash` | 已有 | `Client.NonceAtHash` | 已接入；EIP-1898 hash object |
| `FilterLogs` | 已有 | `Client.FilterLogs` | 入口已接入；逐方法证据仍按本清单验收 |
| `SubscribeTransactionReceipts` | 已有（WSS 单源/多源） | `Client.SubscribeTransactionReceipts` | policy+Next完整连续流已迁移；Head路径、完整块承诺与固定Q官方字段支持分离；2026-11-25本地验收通过，真实provider证据独立 |
| `SubscribeNewHead` | 已验收（WSS 单源/多源） | `Client.SubscribeNewHead`、view同合同 | 固定Tip+DAG、Q路径证人与独立hash对象供应、完整连续delta/reorg、审计及显式预算；`TestSubscribeNewHeadPublicEntrypointsUsePathWitnessesAndHashRecovery`覆盖root/view本地真实WSS，`newhead_integration_test.go`覆盖恢复活性、取消、慢消费、长期回收；2026-11-23全仓普通/race通过，未新增真实provider证据 |
| `SubscribeFilterLogs` | 已完成本地方法验收（2026-11-24） | `Client.SubscribeFilterLogs` / view，同一policy+Next/Basis/Close | 私有Head出生路径、独立版本与队列、完整块撤回和零命中；交易/收据根验证后冻结过滤、构造全部Log定位字段；root/view×单/多源真实WSS、终态审计、预算/生命周期和全仓普通/race通过 |
| `PendingBalanceAt` | 已有 | `Client.PendingBalanceAt` | pending-local；跨 transport 证据待补 |
| `PendingStorageAt` | 已有 | `Client.PendingStorageAt` | pending-local；跨 transport 证据待补 |
| `PendingCodeAt` | 已有 | `Client.PendingCodeAt` | pending-local；跨 transport 证据待补 |
| `PendingNonceAt` | 已有 | `Client.PendingNonceAt` | pending-local；跨 transport 证据待补 |
| `PendingTransactionCount` | 已有 | `Client.PendingTransactionCount` | PendingObserve 多源负向已覆盖；跨 transport 证据待补 |
| `CallContract` | 已有 | `Client.CallContract` | 入口已接入；逐方法证据仍按本清单验收 |
| `CallContractAtHash` | 已有 | `Client.CallContractAtHash` | EIP-1898/跨 transport 证据待补 |
| `PendingCallContract` | 已有 | `Client.PendingCallContract` | pending-local/错误证据待补 |
| `SuggestGasPrice` | 已有 | `Client.SuggestGasPrice` | 入口已接入；逐方法证据仍按本清单验收 |
| `SuggestGasTipCap` | 已有 | `Client.SuggestGasTipCap` | 入口已接入；逐方法证据仍按本清单验收 |
| `BlobBaseFee` | 已有 | `Client.BlobBaseFee` | 入口已接入；逐方法证据仍按本清单验收 |
| `FeeHistory` | 已有 | `Client.FeeHistory` | 入口已接入；逐方法证据仍按本清单验收 |
| `EstimateGas` | 已有 | `Client.EstimateGas` | 入口已接入；逐方法证据仍按本清单验收 |
| `EstimateGasAtBlock` | 已有 | `Client.EstimateGasAtBlock` | 参数/错误/跨 transport 证据待补 |
| `EstimateGasAtBlockHash` | 已有 | `Client.EstimateGasAtBlockHash` | EIP-1898/跨 transport 证据待补 |
| `SendTransaction` | 已有 | `Client.SendRawTransaction` / view SendTransaction | 入口已接入；逐方法证据仍按本清单验收 |
| `SendTransactionSync` | 已有且方法级验收 | `Client.SendTransactionSync` | broadcast+receipt polling；取消/期限/最后尝试与最近观察已验收，不扩张为inclusion/finality/reorg保证 |
| `SendRawTransactionSync` | 已有且方法级验收 | `Client.SendRawTransactionSync` | 同上，四入口共享一次广播和客户端receipt观察 |
| `SimulateV1` | native 入口已接入；语义验收待完成 | `Client.SimulateV1` / view / Raw Call 共用注册计划 | 请求为 `ethapi.SimulateV1Request`，返回 `[]ethapi.SimulateBlock`；base 为 `*ethapi.SimulateBlockReference`，nil 表示 latest；强类型和方法数闭合均不等于逐方法语义验收 |

`go-ethereum/ethclient/gethclient` 还提供扩展方法，例如 `GetProof`、`CallContract` overrides、`Trace*`、`SubscribePendingTransactions` 和调试统计。它们不自动进入 EthClient；必须单独决定是否属于项目的生态承诺。

## 当前 transport 事实

| 能力 | native HTTP | native WSS | 内部 geth oracle | 备注 |
| --- | --- | --- | --- | --- |
| 普通 JSON-RPC request/response | 已有 | 已有 | 差分测试／诊断 | oracle 不在生产执行图中 |
| request ID demux | HTTP 请求级 | 已有 | 由 geth | WSS raw-call 已测试乱序和 pending 清理 |
| `ChainID` / `Close` | 已有 | 已有 | 不适用 | oracle 不提供生产 Client 生命周期 |
| `error.data` presence | 已有四态 | native raw 可保留；provider golden 未闭合 | 诊断参考 | missing/null/value/unknown 必须分开记录 |
| 普通订阅 | 不适用 | 已有 capability 与单源／多源 facade | 未承诺 | capability不扩张unary Transport；NewHead/Logs/Receipts完整连续流及跨入口生命周期已验收；真实provider证据独立 |
| HTTP Pull 补查 | 可用 | 仅当同一 source 有配置 | 不适用 | 不得把任意 HTTP endpoint 当作同一 source |
| reconnect / keepalive / queue | 无通用 replay | 通知条数/字节/单消息与pre-bind总额有界，Release租约覆盖接收方解码；adapter不自动重连，root重建会话 | 不统一承诺 | 生产失败排空和主动关闭丢弃均有反例；不承诺主动keepalive、无限恢复或真实provider支持 |

## 4. 已接入的引用与派生入口

- BalanceAtHash／NonceAtHash／StorageAtHash／CodeAtHash复用现有RPC与plan，显式blockHash对象保留requireCanonical:false；不建立moving reference。
- HeaderByHash／HeaderByNumber已于2026-11-23专项验收：复用block读取并固定fullTx=false，typed交付生成Header；完整头与最小reference身份分开，不宣称body承诺已验证。nil／已知负tag先建立reference，pending header与未知负数本地拒绝；默认strict与finalized preset的差异见reference合同及引用矩阵。
- Pending状态读取、PendingCallContract、PendingTransactionCount已接入；来源可多源收集，结果Selected封顶，不把pending错误信封当mined受保护data。跨传输负例与真实来源仍按证据缺口列账。
- TransactionSender已于2026-11-23专项验收：复用block/index方法计划与生成Transaction，核对输入hash／envelope；官方from声明参与比较，保留来源声明交付，不执行sender recovery或证明链上inclusion。
- AnchorBlockHash／AnchorBlockHashForTag／AnchorBlockHeight已于2026-11-23专项验收：Client独立reference查询，逐调用重建，hash/height与Reference同源，部分失败证据与capture保留；HTTP/WSS、tag、超时/取消/Close等26项普通及25项独立race通过。运行实现保留；显式finalized preset的default/latest例外见reference合同，F1配置组合仍独立。

## 5. 剩余验收

| 范围 | 未关闭内容 |
| --- | --- |
| SimulateV1 | 模拟块与内层结果、顶层错误、完整base/reference与真实provider行为 |
| 跨传输 | 同输入HTTP／raw WSS的成功、null、畸形、冲突、取消、Close、原始材料与错误presence |
| 订阅 | NewHead/Logs/Receipts连续流、证明与资源验收完成；跨入口生命周期于2026-11-25验收，覆盖准入/退休/真实退出及双入口三流本地拒绝；其余S1裁决和真实provider观察仍独立 |
| Provider | WSS error.data:null、syncing对象、权限／方法能力差异、历史分叉与可重跑来源 |
| freshness | F1请求范围、样本有效性、finalized配置组合及真实更新时间分布 |
| 整体实现 | 历史decision梳理、手写定义与逐模块优化；文档完成不代表代码任务完成 |

每项交付值／错误／status／reference／来源归因／材料／生命周期的实际证据，记录命令、退出码和未覆盖范围。离线、本地链和真实provider独立记账；默认测试不联网。具体断言与命令归 [验证合同](method-verification-pipeline.md)，正式质量／race／fuzz／发布门归 [发布合同](release-contract.md)。

当前Sender、Header、Anchor、Logs、Receipts及跨入口生命周期均已验收，随后按路线图执行全仓质量治理。各方法的定向证据不替代全仓发布、Anvil或公网采集；历史测试通过不代替目标提交结果。

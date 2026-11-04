# 方法执行计划清单（23 个 RegisteredMethod 的 plan 与 typed path 现状）

状态：**已登记（2026-11-03）。本文件是方法对接工作的事实基线：每个
Registered read 方法的 ReadPlan、resolver、admission、reference、pending、
projector、typed path 与证据指针，逐项来自
`rpc/methods/spec_inventory.go`、`rpc/methods/catalog.go`、根 facade 与
根包的当前代码。本文只登记现状，不新增方法、不改变裁决语义。**

## 0. 归属规则（验收前提）

- **唯一业务语义 owner**：`rpc/methods` 的冻结 spec inventory。每个方法一个
  `MethodSpec`，`CompilePlan` 编译为 `internal/runtime` 安装的 ReadPlan。
  互锁：`TestCatalogFamilyCoverageInterlock`（spec ↔ catalog 一一对应、
  无重复、adapter 映射必须有 alignment 记录）。
- **没有方法只存在于旧 chain**：legacy decorator constructor 仅为兼容与
  等价验证 seam（`plan_equivalence_test.go`、`plan_*_equivalence_test.go`
  把 hand-written constructor 与 spec 编译的 plan 逐项绑定）；默认 runtime
  只安装 registry（`internal/runtime/method_registry.go`），生产路径不再
  以 decorator chain 为事实来源。
- **生产路径不新增 decorator 组合**：registry 是唯一组装点；新增组合必须
  先改 spec inventory 并过等价门。
- **legacy chain 仅作对照**，不是 runtime 事实来源。
- 证据等级用词沿用 [方法对接卡片](method-integration-cards.md)：
  `soak/sample/object 形状证据/unverified/assumed`；`未观察` 项不得用合成
  样本关闭。

## 1. 23 方法状态表

约定：resolver `strict`（单值共识路径）/`median`（数值中位数）/
`local`（本地观察）/`call-revert`（revert 语义感知）；admission `—` 表示
仅默认 wire 校验；reference/pending 列为 spec 声明；projector 为观察投影
ID；typed path 列出 root facade 的类型化入口，
`通用 Call` 表示仅经 `Client.Call` 到达。

| 方法 | resolver | normalize/encode | admission | reference | pending | projector | root typed |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `eth_chainId` | strict | quantity | — | — | — | — | `ChainID` |
| `eth_blockNumber` | median | quantity | — | — | — | — | `BlockNumber` |
| `eth_gasPrice` | median | quantity | — | — | — | — | `SuggestGasPrice` |
| `eth_maxPriorityFeePerGas` | median | quantity | — | — | — | — | `SuggestGasTipCap` |
| `eth_blobBaseFee` | median | quantity | — | — | — | — | `BlobBaseFee` |
| `net_version` | strict | decimal string | — | — | — | — | `NetworkID` |
| `net_peerCount` | local | folded quantity | — | — | — | — | `PeerCount` |
| `net_listening` | local | canonical bool | — | — | — | — | —（通用 `Call`） |
| `eth_syncing` | local | progress object | — | — | — | — | `SyncProgress` |
| `eth_getBalance` | strict | quantity | — | hash@pos1 (EIP-1898) | observe pos1 | scalar-standard | `BalanceAt` |
| `eth_getTransactionCount` | strict | quantity | — | hash@pos1 (EIP-1898) | observe pos1 | — | `NonceAt` |
| `eth_getStorageAt` | strict | storage word | — | hash@pos2 (EIP-1898) | observe pos2 | — | `StorageAt` |
| `eth_getCode` | strict | hex data | — | hash@pos1 (EIP-1898) | observe pos1 | — | `CodeAt` |
| `eth_call` | call-revert | call outcome | — | hash@pos1 (EIP-1898) | observe pos1 | scalar-standard | `CallContract` |
| `eth_estimateGas` | median | quantity | — | hash@pos1 (EIP-1898) | observe pos1 | — | `EstimateGas` |
| `eth_createAccessList` | strict | access list object | — | hash@pos1 (EIP-1898) | observe pos1 | — | — |
| `eth_getBlockByHash` | block | block identity | block admission | — | — | block | `BlockByHash` |
| `eth_getBlockByNumber` | block | block identity | block admission | — | observe pos0 | block | `BlockByNumber`（含负数分支） |
| `eth_getTransactionByHash` | strict | tx object | tx admission | — | — | transaction | `TransactionByHash` |
| `eth_getTransactionReceipt` | strict | receipt object | receipt admission | — | — | receipt | `TransactionReceipt` |
| `eth_getBlockReceipts` | strict | block receipt list | block-receipts admission | hash@pos0 | refuse pos0 | block-receipts | `BlockReceipts` |
| `eth_getLogs` | strict | logs list | logs admission | height fields@param0 | refuse pos0 | logs | `FilterLogs` |
| `eth_feeHistory` | strict | fee history object | request+response admission | height@pos1 | observe pos1 | fee-history | `FeeHistory` |

写路径（非本表 read 范围）：`eth_sendRawTransaction` 走统一 Source 固定成员
`Broadcast`（root `SendRawTransaction` / `SendTransaction`；决策 0064），
不进入 ReadPlan registry。`eth_getProof` 为显式 Unsupported。

## 2. 逐项验收（基线出口）

| 验收项 | 结论 | 锁定 |
| --- | --- | --- |
| 没有方法只存在于旧 chain 而无明确 plan | 23/23 方法 spec ↔ registry ↔ catalog 对齐 | `TestCatalogFamilyCoverageInterlock`、`TestSpecMasksCoverCatalog`、plan 等价族 |
| 没有方法被错误标记为已完成 | 证据状态只引用卡片总表；`未观察`/`assumed` 保留原状 | 本表 typed path 列与卡片总表交叉核对（2026-11-03） |
| 每个方法有唯一业务语义 owner | spec inventory 是唯一 owner；resolver wrapper 属 resolver seam（0063 保留深模块） | `resolver_capability_interlock_test.go` |
| 生产路径不新增 decorator 组合 | registry 单点组装；无第二组装点 | `internal/runtime/method_registry.go` 注释契约 + 架构互锁 |

## 3. H1–H7 出口状态（继承卡片总表，2026-10-31 证据）

- **H1 scalar/network**：9 方法全部有 typed path 与 protocol-error/presence
  测试（`admission_suite_test.go`、方法级测试文件）。缺口（未观察）：
  `eth_syncing` progress object 真实样本（历史观察仅 `false`）、
  `eth_blobBaseFee` 多源支持面普查。
- **H2 EIP-1898 anchored state**：4 方法 spec 均声明 `ReferenceShapeHashAt`
  + EIP-1898；latest/safe/finalized 豁免 latest tip gate（F1/F2 已接线，
  2026-11-02）。缺口（未观察）：其他历史高度、archive/pruned provider
  差异、pinned request 与业务轮固定 basis 的组合证据。
- **H3 execution**：revert code/message、missing/null/`0x`/value、4e data
  维度、EIP-1898 object、provider rejection ≠ transport failure 均有测试
  （`eth_call_revert_test.go`、`call_revert_4e_test.go`、
  `call_adapter_boundary_test.go`）。缺口（未观察）：pending/local 观察
  的真实 provider 证据。
- **H4 block/tx/receipt**：identity、admission、NotFound、withdrawals/
  uncle/交易根、full tx 字段、BlockRef 与 typed decode 有测试。缺口
  （未观察）：uncle/withdrawals 内容承诺、full transaction 字段跨客户端
  差异、hash 位置接受度普查（2026-10-31 已有 4/4 拒收单点）。
- **H5 logs/feeHistory**：range/hash selector、pending rejection、
  oldestBlock、reward shape、canonical output 有测试。缺口（未观察）：
  大范围 split 与 provider 上限、percentile 舍入跨客户端普查。
- **H6 证据收尾**：按卡片总表逐项推进；规则不变——capture/replay/live
  分级、不用合成 capture 关 provider gap、不提交公共网络交易、未观察项
  显式标记。provider evidence 不改变 method semantics、quorum 或
  threshold。
- **H7 完整替代出口**：逐项核对——23/23 稳定 ReadPlan ✅；root/
  typed 路径逐项可运行 ✅；HTTP 与 WSS 共用同一业务语义（同一 rpcwire/
  ReadPlan；WSS wire 已自有化，2026-11-03）✅；错误类别、NotFound、
  pending、EIP-1898、presence、typed projection 有测试 ✅；legacy
  constructor 仅兼容/等价 seam ✅；默认 runtime 不依赖 decorator chain ✅；
  transport 不决定方法行为（Port + 中立哨兵；架构互锁锁定）✅；
  test/race/vet/architecture/Verification 门 ✅；文档、决策、方法卡片、
  roadmap 状态一致 ✅（本文为冻结锚点）。

本清单登记后，后续推进按"单一业务意图"逐卡提交（对应路线图 H1–H6 各
行）；证据缺口只由真实 capture/replay/live 关闭，不以本文或进程内样本
代替。

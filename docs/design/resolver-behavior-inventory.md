# Resolver Behavior Inventory（RS0：resolver 行为事实盘点）

> 状态：已登记（2026-10-31，RS0 交付物）。本文是 resolver seam 主线的逐方法
> 事实记录，对应路线图 `project-roadmap.md` 的 Resolver seam 任务表。范围：
> 23 个 Registered read 方法；write 管线、`eth_getProof`（unsupported）、
> 流与派生族不在 read registry 内，不逐列。
>
> 盘点只登记事实，不做结论。是否保留深模块、迁移 wrapper 或构造 ResolverPlan，
> 由 RS1–RS5 按本文与后续证据裁决（停止条件见路线图）。

## 1. 两层事实（宣言）

- **默认 runtime registry 由 `rpc.ReadPlan` 驱动**：每个 Registered read 一个
  显式计划，`buildMethodRegistry()` 从 `Specs()` 编译；resolver 值由
  `CompilePlan`→`assembleResolver` 组装，capability 由计划路径按 resolver 值
  自己的接口读取。registry 里没有 decorator chain。
- **legacy constructor 不是 runtime 事实来源**：`rpc/methods/eth_*.go` 的
  `XxxPolicy(anchor, …)` 只剩兼容 facade 与 plan/chain 等价测试的对照实现。
  没有任何 Registered method 因"有 constructor"而在 runtime 走 chain；
  也没有任何方法因"走 plan"而宣称 resolver 已扁平化——resolver 本体
  （`rpc.Strict`/`Median`/`Local`/CallRevert/`blockPolicy`）一个未动。

## 2. Capability 事实（按 resolver 本体）

`ReadPlan.Resolver` 携带的 resolver 值只有五个形态，capability 以它们的
Go 接口实现为准（不是按方法逐个声明）：

| resolver 本体 | 构造点 | Resolve | ResolveCorroborated | OutcomeResolver（含 per-source verdict） | CorroboratingOutcomeResolver | RequiresAgreement | IncludeRPCError | MethodRuleSource |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `rpc.Strict`（strictFunc） | assembleResolver | ✓ | ✓ | ✓ | ✓ | ✓ | 仅 `WithRPCErrorBodies`（本目录只有 eth_call） | ✓ |
| `blockPolicy{strict}` | assembleResolver（`ResolverBlock`） | ✓（含 header-only Value 恢复） | ✓ | ✓ | ✓ | ✓ | —（strict 未开启） | ✓（来自内层 strictFunc） |
| `rpc.NewCallRevertPolicy` | assembleResolver（`ResolverCallRevert`） | ✓ | ✓ | ✗（无 `ResolveOutcome`，Outcome verdict 不存在于该类） | ✗ | ✓ | ✓（构造即开启） | ✓ |
| `rpc.Median` | assembleResolver | ✓（Selected 封顶） | ✗ | ✗ | ✗ | ✗ | — | ✓（quantity） |
| `rpc.Local` | assembleResolver | ✓（Selected 封顶） | ✗ | ✗ | ✗ | ✗ | — | ✓（构造声明 compare kind） |

关键不对称（RS1/RS3 的输入，不是本轮缺陷）：

- legacy wrapper 的 method-specific capability 只在 chain 上存在：
  `logsPolicy`（Prepare/ReferenceFor/BlockRef/MethodRule 覆写）、
  `feeHistoryPolicy`（Prepare、admission）、`blockPolicy` 的
  admission/Value 恢复。计划路径用 `Spec.Validate`/`Spec.Admit` hook 与计划
  声明（BlockRef pin、Validate 时机）替代了它们，capability 归一到
  resolver 本体。
- `feeHistoryPolicy` 没有 `ResolveOutcome`（只有 corroborated 变体）；
  计划路径的 plain strict 有全部 Outcome capability。等价测试已经覆盖该
  差异不改行为，但它是 seam 需要明确"以哪一侧为准"的实例。
  （RS1 修订：该差异描述的是 wrapper 层**自行声明**的方法集；组装后的
  chain 经 `rpc.Decorator` 条件转发同样回答 plain `ResolveOutcome`，
  公开行为与计划路径等价。裁决见
  [`resolver-seam-contract.md`](resolver-seam-contract.md) §4。）
- `IncludeRPCError`：全目录只有 eth_call 的 resolver 开启（strict 与
  call-revert 同一决定）；legacy `logsPolicy` 的转发实现其内层 strict 也
  未开启，两路一致为 false。

**网络 callback**：计划不携带任何网络回调——reference 数据以 established
evidence 值到达，admission caller 由 coordinator 注入。legacy constructor
签名中的 `AnchorFunc`/`AnchorHeightFunc` 是 stage 发放的消费型 reader，
仍属 seam 携带的 callback 形态；这正是 quarantine(RS4) 要钉住的边界。

## 3. 逐方法清单

列含义：plan resolver kind/value 为计划路径事实；legacy constructor 为 chain
对照实现；callback 指构造参数携带的网络回调；Out./Corr. 为 Outcome 与
Corroboration capability（按 §2，取 resolver 本体 + 计划 hook 后的等效）；
resolver rule 列 = `MethodRule` 的 `Resolver/CompareKind/KeySource/Result`；
anchor/pending 由计划声明（`ReadPlan.MethodRule()` 投影），不在 resolver 列内。

| method | plan resolver kind | plan resolver value | legacy constructor | callback | Out. | Corr. | resolver rule（Resolver/Compare/Key/Result） | projector ID | typed path | evidence status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| eth_chainId | strict | `Strict(NormalizeWireQuantity; EncodeQuantity)` | `ChainIDPolicy`（无 decorator 层） | 无 | ✓ | ✓ | strict/quantity/canonical-bytes/canonical | — | `Client.ChainID` | soak census ✅ |
| eth_getBlockByHash | block | `blockPolicy{byHash}∘Strict(normalizeBlockIdentity; canonicalObject blockobject.Encode)` | `BlockByHashPolicy`（同构） | 无 | ✓ | ✓ | strict/method-specific/normalizer/canonical | `block` | `Client.BlockByHash`（adapter 分流专用方法） | sample 2026-09-23 block 形状，非普查 |
| eth_getBlockByNumber | block | `blockPolicy{byHash:false}∘Strict(normalizeBlockIdentity; canonicalObject blockobject.EncodeByNumber)` + pending observe 0 | `BlockByNumberPolicy`（PendingLocal(0)∘blockPolicy） | 无 | ✓ | ✓ | strict/method-specific/normalizer/canonical | `block` | `Client.BlockByNumber`（负 tag 先建立后取 hash） | soak 建立每轮 ✅；hash-位置 ⚠（3/4 拒收；2026-10-31 3 拒收 + 1 限流） |
| eth_getBalance | strict | `Strict(NormalizeWireQuantity; EncodeQuantity)` | `GetBalancePolicy`(Anchored∘EIP1898∘PendingLocal) | legacy `AnchorFunc` | ✓ | ✓ | strict/quantity/canonical-bytes/canonical | `scalar-standard` | `Client.BalanceAt` | soak 2026-09-05 ✅；object 形状证据；2026-10-31 latest object 4/4 |
| eth_getTransactionReceipt | strict | `Strict(normalizeReceiptObject; receipt.Encode)` | `TransactionReceiptPolicy` + `policy.WithAdmission` | 无（admission 运行期注入） | ✓ | ✓ | strict/method-specific/canonical-bytes/canonical | `receipt` | `Client.TransactionReceipt` | sample 2026-09-23 |
| eth_getTransactionByHash | strict | `Strict(normalizeTransactionObject; canonicalObject transaction.Encode)` | `TransactionByHashPolicy` + `WithAdmission` | 无 | ✓ | ✓ | strict/method-specific/normalizer/canonical | `transaction` | `Client.TransactionByHash` | sample 2026-09-23 |
| eth_getBlockReceipts | strict | `Strict(normalizeBlockReceiptList; receipt.EncodeBlock)` | `BlockReceiptsPolicy`(RejectPending(0)∘Anchored(0)∘WithAdmission) | legacy `AnchorFunc` | ✓ | ✓ | strict/method-specific/canonical-bytes/canonical | `block-receipts` | `Client.BlockReceipts` | sample + soak 2026-09-05 ✅ |
| eth_getTransactionCount | strict | 同 getBalance | `GetTransactionCountPolicy` | legacy `AnchorFunc` | ✓ | ✓ | strict/quantity/canonical-bytes/canonical | — | `Client.NonceAt` | soak ✅；object 形状 sample 2026-09-23（单源成功）；2026-10-31 latest object 4/4 |
| eth_getStorageAt | strict | `Strict(NormalizeStorageWord; EncodeStorageWord)` | `GetStorageAtPolicy`(pos 2) | legacy `AnchorFunc` | ✓ | ✓ | strict/method-specific/canonical-bytes/canonical | — | `Client.StorageAt` | soak ✅；object 形状 sample 2026-09-23（单源成功）；2026-10-31 latest object 4/4 |
| eth_getCode | strict | `Strict(NormalizeHexData; EncodeData)` | `GetCodePolicy` | legacy `AnchorFunc` | ✓ | ✓ | strict/method-specific/canonical-bytes/canonical | — | `Client.CodeAt` | soak ✅；object 形状 sample 2026-09-23（单源成功）；2026-10-31 latest object 4/4 |
| eth_call | call-revert | `NewCallRevertPolicy(normalizeCallOutcome; RPCErrorBodies + canonical encodeCallSuccess)` | `CallPolicy`(WithAnchor∘EIP1898∘PendingLocal) | legacy `AnchorFunc` | Resolve/Corroborated ✓，Outcome ✗（§2） | ✓（agreement） | call-revert/method-specific/—(call-revert 不声明 key source)/canonical | `scalar-standard` | `Client.CallContract`/`Call` | soak 2/0/1 ✅；revert-data sample 2026-09-23；WSS 2026-10-31 missing/`0x`/value |
| eth_estimateGas | median | `Median(NormalizeWireQuantity; canonical EncodeQuantity)` | `EstimateGasPolicy`(WithAnchor∘EIP1898∘PendingLocal) | legacy `AnchorFunc` | ✗ | ✗ | median/quantity/—/canonical | — | `Client.EstimateGas` | soak ✅；object 形状证据 |
| eth_getLogs | strict | `Strict(normalizeLogs; log.Encode)` | `LogsPolicy`（logsPolicy wrapper 全套覆写） | legacy `AnchorHeightFunc` | ✓ | ✓ | strict/method-specific/canonical-bytes/canonical | `logs` | `Client.FilterLogs` | soak 2026-09-05 ✅（70 logs） |
| eth_feeHistory | strict | `Strict(normalizeFeeHistory; feehistory.Encode)` | `FeeHistoryPolicy`(PendingLocal∘WithAnchorHeight(feeHistoryPolicy∘Strict)) | legacy `AnchorHeightFunc` | 仅 corroborated 变体（wrapper 无 `ResolveOutcome`） | ✓（agreement） | strict/method-specific/canonical-bytes/canonical | `fee-history` | `Client.FeeHistory` | soak ✅；2026-10-31 3/4 同一 `oldestBlock` 一致，1 个源区间不同；固定历史区间 [1,33,67,99] 4/4 一致 |
| eth_gasPrice | median | `Median(NormalizeWireQuantity; canonical EncodeQuantity)` | `GasPricePolicy`（无层） | 无 | ✗ | ✗ | median/quantity/—/canonical | — | `Client.SuggestGasPrice`（typed `GasPrice`） | soak census ✅ |
| eth_maxPriorityFeePerGas | median | `Median(同上)` | `MaxPriorityFeePerGasPolicy` | 无 | ✗ | ✗ | median/quantity/—/canonical | — | `Client.SuggestGasTipCap` | soak census ✅ |
| eth_blobBaseFee | median | `Median(同上)` | `BlobBaseFeePolicy` | 无 | ✗ | ✗ | median/quantity/—/canonical | — | `Client.BlobBaseFee` | sample 2026-10-21；2026-10-31 live probe 2/4 成功、1 free-plan、1 策略拒绝 |
| eth_blockNumber | median | `Median(同上)` | `BlockNumberPolicy` | 无 | ✗ | ✗ | median/quantity/—/canonical | — | `Client.BlockNumber`（派生按 corroborated reference） | soak ✅ |
| eth_createAccessList | strict | `Strict(normalizeAccessList; canonical accesslist.Encode)` | `CreateAccessListPolicy`(Anchored∘EIP1898∘PendingLocal) | legacy `AnchorFunc` | ✓ | ✓ | strict/method-specific/canonical-bytes/canonical | — | `Client.CreateAccessList` | object 形状 2026-09-23；2026-10-31 latest object 4/4，非兼容结论 |
| net_peerCount | local | `Local(NormalizeHexQuantity; quantity; folded canonical)` | `PeerCountPolicy` | 无 | ✗ | ✗ | local/quantity/—/canonical | — | `Client.PeerCount` | soak census ✅ |
| eth_syncing | local | `Local(normalizeSyncing; object; EncodeCanonicalJSON)` | `SyncingPolicy` | 无 | ✗ | ✗ | local/object/—/canonical | — | `Client.SyncProgress` | soak census ✅；2026-10-31 4/4 为 `false`，progress object 未观察 |
| net_version | strict | `Strict(NormalizeDecimalString; EncodeDecimal)` | `NetVersionPolicy`（无层） | 无 | ✓ | ✓ | strict/quantity/canonical-bytes/canonical | — | `Client.NetVersion`（root-only） | soak census ✅ |
| net_listening | local | `Local(normalizeListening; object; EncodeCanonicalJSON)` | `ListeningPolicy` | 无 | ✗ | ✗ | local/object/—/canonical | — | `Client.Listening`（root-only） | soak census ✅ |

anchor/pending 的方法级声明（不属 resolver 列，登记处）：
`Spec.Reference`/`Spec.Pending` 与 `ReadPlan.MethodRule()` 投影，见
`moving-tag-matrix.md`；projector ID 的绑定互锁见
`internal/runtime/projector_registry_test.go`。

## 4. RS0 验收对照

- 23 个 Registered read 逐项登记 ✅（§3）。
- 没有方法因"有 constructor"被误判 runtime 走 chain ✅（§1；runtime 只从
  plans 装配，interlock 兼测试 `TestDefaultBuild`/`ValidateReadCoverage`）。
- 没有方法因"走 plan"被误判 resolver 已扁平化 ✅（§2：5 个 resolver 本体
  原样保留；plan 仅替换阶段编排上的 wrapper/capability 形态）。
- resolver 列来源单一：plan 路径读 resolver 本体接口；chain 路径读 wrapper
  转发。清单中的 capability 均以 Go 接口实现为据，无臆测。

## 5. 交给 RS1–RS3 的未决问题（登记，不裁决）

> RS1 已设 [`resolver-seam-contract.md`](resolver-seam-contract.md) 裁决
> 1、2、5；3 归 RS2，4 归 RS4。

1. `CallRevert` 缺 `ResolveOutcome`/`ResolveOutcomeCorroborated`：per-source
   verdict 在该类上是否应有统一 Outcome 形态，或由 RS1 的最小契约显式
   "call-revert 无 verdict"化。
   —— RS1 裁决：统一 Outcome，verdict 按 execution 维度陈述；data 维度
   状态留在 Dimensions/Revert/Provenance。契约与测试见
   [`resolver-seam-contract.md`](resolver-seam-contract.md) §3、
   `rpc/methods/call_revert_outcome_test.go`。
2. `feeHistoryPolicy` 只有 corroborated Outcome：最小契约应说明 outcome
   verdict 能力是否是 resolver 本体（strict）统一拥有，wrapper 差异只属于
   legacy。

3. `blockPolicy` 的 header-only Value 恢复与 full-block admission：RS2 已裁决
   当前阶段保留深模块；计划与 legacy admission 共用 `admitBlockResponse`，
   `byHash` 身份由编译事实绑定，不从请求方法名推断。若后续出现 wrapper
   携带 Prepare/reference/network 控制或等价矩阵分叉，再由 RS5 修订该裁决。
4. `logsPolicy`/`feeHistoryPolicy` 的 Prepare/reference/blockRef 覆写已由
   计划 hook 替代；删除条件随 RS4 quarantine 的兼容范围一并裁决。
5. Median/Local 的"无 corroboration 声明"与 `ResolverKind` 的
   `corroboratingResolvers()` 决定表保持一致；agreement capability 也必须与
   该类别同步。RS1/RS3 裁决：保留 switch 为协议级声明；能力=resolver 本体
   方法集，编译期双向绑定 + 装配期一次性读取，不新增 marker 接口。见契约 §5、
   `rpc/methods/resolver_capability_interlock_test.go` 与
   `aggregator/resolver_capability_interlock_test.go`。

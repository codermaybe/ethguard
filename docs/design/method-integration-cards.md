# 方法对接卡片（23 个 RegisteredMethod）

状态：**卡片已建立（2026-10-31）**。本文件按业务语义为每个已注册
read 方法维护一张对接卡片，记录 root、typed、错误、pending、
EIP-1898、reference 与证据等级。它不新增方法、不改变裁决语义，也不把
离线测试或进程内样本升级为 provider 兼容性结论。

证据等级用词（与 `resolver-behavior-inventory.md` 一致）：

- `soak census ✅`：live soak 已覆盖该方法每轮/普查，但不等于长期兼容性证明；
- `soak <日期> ✅`：指定日期的 live soak 观测；
- `sample <日期>`：真实响应样本，非普查；
- `object 形状证据`：只证明对象形状，不是兼容结论；
- `unverified`：尚无 provider 证据；
- `assumed`：实现假设，不能据此声称已验证。

通用事实（不逐卡重复）：

- 23 个方法全部是 Registered read；`eth_getProof` 是 Unsupported，其他
  写/订阅/trace 方法保持 Forwarded 或 Unsupported 边界；
- root `Client.Call` 是通用入口；下文的 root 类型化入口只是便捷封装，
  共享同一 Decision；
- typed 边界要求 Corroborated（协证要求由 invocation 承载：类型化方法经
  `aggregator.AggregateStrict`，门读 `Invocation.RequireCorroboration`）；
  Selected 不静默替代；
- 固定分母、quorum、reference、EIP-1898 改写和 pending 语义由 ReadPlan
  声明，卡片只登记现状，不重新定义。

## 证据状态总表（2026-10-31）

`capture` 是已归档的 redacted batch；`replay` 表示该 batch 有 expect 回放；
`live` 是真实 provider 的观测，不升级为长期兼容性结论。`—` 表示该项没有
独立证据。具体字段和待办见各卡片。

| 方法 | provider capture | replay | live observation | 尚未验证 |
| --- | --- | --- | --- | --- |
| `eth_chainId` | read-local 2026-10-19 | ✅ | soak census | — |
| `eth_blockNumber` | read-local 2026-10-19 | ✅ | soak ✅ | — |
| `eth_gasPrice` | read-volatile 2026-10-21 | ✅ | soak census | — |
| `eth_maxPriorityFeePerGas` | read-volatile 2026-10-21 | ✅ | soak census | — |
| `eth_blobBaseFee` | read-volatile 2026-10-21 | ✅ | 2026-10-31 probe：2/4 成功，1 free-plan，1 非 EVM 策略拒绝 | 多源支持面普查 |
| `net_peerCount` | read-local 2026-10-19 | ✅ | soak census | — |
| `eth_syncing` | read-volatile 2026-10-21（仅 `false`） | ✅（false） | soak + 2026-10-31 probe：全部 `false` | progress object 形状 |
| `net_version` | read-local 2026-10-19 | ✅ | soak census | — |
| `net_listening` | read-local 2026-10-19 | ✅ | soak census | — |
| `eth_getBalance` | eip1898-object 2026-09-23 | ✅ | soak 2026-09-05；2026-10-31 latest object 4/4 | — |
| `eth_getTransactionCount` | eip1898-object 2026-09-23 | 回放未达 corroboration | 2026-10-31 latest object 4/4；历史 object 4/4（block 18,000,000） | 其他历史高度/archive 策略 |
| `eth_getStorageAt` | eip1898-object 2026-09-23 | 回放未达 corroboration | 2026-10-31 latest object 4/4；历史 object 4/4（block 18,000,000） | 其他历史高度/archive 策略 |
| `eth_getCode` | eip1898-object 2026-09-23 | 回放未达 corroboration | 2026-10-31 latest object 4/4；历史 object 4/4（block 18,000,000） | 其他历史高度/archive 策略 |
| `eth_call` | revert-data 2026-09-23；eip1898-object | ✅ | soak 2/0/1；revert sample；WSS 2026-10-31 missing/`0x`/value | WSS `null`；WSS 四态 golden |
| `eth_estimateGas` | eip1898-object 2026-09-23 | ✅ | soak ✅；object 形状证据 | latest/历史 object 的多源形状 |
| `eth_createAccessList` | eip1898-object 2026-09-23 | ✅ | 2026-10-31 latest object 4/4；历史 object 4/4，结果一致；blockHash/canonical 4/4（单块） | hash/canonical 跨高度普查 |
| `eth_getBlockByHash` | block-receipt-tx 2026-09-23 | ✅ | sample 2026-09-23 | uncle/withdrawals/交易根内容承诺 |
| `eth_getBlockByNumber` | block-receipt-tx 2026-09-23 | ✅ | soak 每轮；2026-10-31 hash 位置 4/4 拒收；历史 header/full 4/4 | hash 位置接受度；uncle 内容；full tx 字段差异 |
| `eth_getTransactionReceipt` | block-receipt-tx 2026-09-23 | ✅ | sample 2026-09-23 | 跨客户端字段差异 |
| `eth_getTransactionByHash` | block-receipt-tx 2026-09-23 | ✅ | sample 2026-09-23 | 未知 typed tx 分布 |
| `eth_getBlockReceipts` | block-receipt-tx 2026-09-23 | ✅ | sample + soak 2026-09-05 | — |
| `eth_getLogs` | read-blockref 2026-10-21 | ✅ | soak 2026-09-05（70 logs） | 大范围 split 与 provider 上限 |
| `eth_feeHistory` | read-blockref 2026-10-21 | ✅ | soak ✅；2026-10-31 3/4 同一 oldestBlock 一致，1 个源区间不同；固定历史区间 [1,33,67,99] 4/4 一致 | percentile 舍入的跨客户端普查 |

## 1. 确定性与本地方法

### eth_chainId

- Root：`Client.ChainID`（typed `*big.Int`）。
- Adapter：`ChainID`（支持）。
- Typed：`decodedQuantity`；无对象 decoder。
- 错误：`ErrConflict` / `ErrNoValidResponse` / typed decode；无 NotFound。
- Pending：无。
- EIP-1898：不适用。
- Reference：无（stateless）。
- 证据：`soak census ✅`。
- 待办：无 provider 形状缺口。

### eth_blockNumber

- Root：`Client.BlockNumber`（派生：先建立 corroborated 参考，再按 hash 取块）。
- Adapter：`BlockNumber`（委托 root 派生）。
- Typed：`DecodeBlockWithEvidence` + 高度读取。
- 错误：reference/block 错误；无独立 blockNumber fan-out。
- Pending：无。
- EIP-1898：不适用。
- Reference：latest 参考（或 finalized anchoring 预设）。
- 证据：`soak ✅`。
- 待办：不得改写为 provider tip 的 raw fan-out。

### eth_gasPrice

- Root：`Client.GasPrice`（typed `*big.Int`）。
- Adapter：`SuggestGasPrice`（支持）。
- Typed：`decodedQuantity`。
- 错误：median 路径的 `ErrNoValidResponse` / typed decode。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`soak census ✅`。

### eth_maxPriorityFeePerGas

- Root：`Client.MaxPriorityFeePerGas`（typed `*big.Int`）。
- Adapter：`SuggestGasTipCap`（支持）。
- Typed：`decodedQuantity`。
- 错误：同 gasPrice。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`soak census ✅`。

### eth_blobBaseFee

- Root：`Client.BlobBaseFee`（typed `*big.Int`）。
- Adapter：无（root-only）。
- Typed：`decodedQuantity`。
- 错误：median 路径的 `ErrNoValidResponse` / typed decode。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`sample 2026-10-21`（`read-volatile` 三次抓取：两个源返回 `0x` quantity，
  一个源返回 free-plan 错误）；2026-10-31 live probe 为 2/4 成功、1 free-plan、
  1 个非 EVM 策略拒绝。公开 capture 与 replay case 已归档；不是普查，也不是
  通用兼容结论。
- 待办：provider 支持面存在差异；不得按方法存在即宣称兼容。

### net_peerCount

- Root：`Client.PeerCount`（typed `uint64`）。
- Adapter：`PeerCount`（支持）。
- Typed：`decodedQuantity` + uint64 范围检查。
- 错误：Local resolver 的 `ErrNoValidResponse` / typed decode。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`soak census ✅`。

### eth_syncing

- Root：`Client.Syncing`（typed `*ethereum.SyncProgress`）。
- Adapter：`SyncProgress`（支持）。
- Typed：`syncProgressWire` → Geth `SyncProgress`；`false` 映射 nil。
- 错误：`true`/null/错误顶层形状 → `ErrMalformedResponse`；嵌套成员类型错误在 typed 边界。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`soak census ✅`；`read-volatile 2026-10-21` 三个源均返回 `false`，
  2026-10-31 live probe 的四个源也全部返回 `false`，未覆盖 progress object。
  progress object 形状仍是 capture gap，T09 未合成。
- 待办：progress object 的真实 provider 形状不得用合成样本关闭。

### net_version

- Root：`Client.NetVersion`（typed `*big.Int`）。
- Adapter：无（root-only）。
- Typed：`decodedQuantity`。
- 错误：strict resolver 的 `ErrConflict` / `ErrNoValidResponse`；typed decode。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`soak census ✅`。

### net_listening

- Root：`Client.Listening`（typed `bool`）。
- Adapter：无（root-only）。
- Typed：直接 `json.Unmarshal`；拒绝 null/非 bool。
- 错误：`ErrMalformedResponse`；Local resolver 的 `ErrNoValidResponse`。
- Pending：无。
- EIP-1898：不适用。
- Reference：无。
- 证据：`soak census ✅`。

## 2. Anchored state

### eth_getBalance

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`BalanceAt`（支持，typed `*big.Int`）。
- Typed：`hexutil.Big`。
- 错误：`ErrConflict` / `ErrNoValidResponse`；typed decode；无 NotFound。
- Pending：`PendingObserve`（pos 1），本地观测为 Selected。
- EIP-1898：支持（object 形式）。
- Reference：`hash-at` pos 1（moving tag 先建立参考）。
- 证据：`soak 2026-09-05 ✅`；EIP-1898 object 形状证据；2026-10-31 latest
  object live probe 4/4 成功；同日历史 block 18,000,000 的 `blockNumber`
  object 与 `blockHash` object 均 4/4 成功且值一致；`blockHash` 带
  `requireCanonical:true` 的 captured run 为 3/4，余下 1 个为 HTTP 499
  空响应（同日直接 probe 为 4/4）。
- 待办：历史 object 的可用性仍取决于 provider archive 策略，不能据此声称任意
  历史高度或任意 provider 都支持。

### eth_getTransactionCount

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`NonceAt`（支持，typed `uint64`）。
- Typed：`hexutil.Uint64`。
- 错误：strict 路径；typed decode。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`soak ✅`；`eip1898-object 2026-09-23` 已归档 `{"blockNumber":...}` 请求，
  仅一个源成功返回 quantity，两个源报告 pruned state，一个源为空；2026-10-31
  latest object live probe 4/4 成功；同日历史 block 18,000,000 的
  `blockNumber` object 4/4 成功且值一致。
- 待办：历史 object 的可用性仍取决于 provider archive 策略，不能据此断言任意
  历史高度或任意 provider 都支持。

### eth_getStorageAt

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`StorageAt`（支持，typed `[]byte`）。
- Typed：`EncodeStorageWord` 对应的 typed 解码。
- 错误：strict 路径；malformed word → `ErrMalformedResponse`。
- Pending：`PendingObserve`（pos 2）。
- EIP-1898：支持。
- Reference：`hash-at` pos 2。
- 证据：`soak ✅`；`eip1898-object 2026-09-23` 的 `{"blockNumber":...}` 请求只有
  一个源成功返回 storage word，两个源报告 pruned state，一个源为空；2026-10-31
  latest object live probe 4/4 成功；同日历史 block 18,000,000 的
  `blockNumber` object 4/4 成功且值一致。
- 待办：历史 object 的可用性仍取决于 provider archive 策略，不能据此断言任意
  历史高度或任意 provider 都支持。

### eth_getCode

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`CodeAt`（支持，typed `[]byte`）。
- Typed：`EncodeData` 对应的 typed 解码。
- 错误：strict 路径；malformed data → `ErrMalformedResponse`。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`soak ✅`；`eip1898-object 2026-09-23` 的 `{"blockHash":...}` 请求只有
  一个源成功返回 code，两个源报告 pruned state，一个源为空；2026-10-31
  latest object live probe 4/4 成功；同日历史 block 18,000,000 的
  `blockNumber` object 4/4 成功且 code 一致。
- 待办：历史 object 的可用性仍取决于 provider archive 策略，不能据此断言任意
  历史高度或任意 provider 都支持。

### eth_call

- Root：`Client.Call`（通用；返回 Decision，revert 由 adapter 解码）。
- Adapter：`CallContract`（支持；`RevertError`）。
- Typed：`decodeCallDecision` → success DATA / `RevertError`。
- 错误：execution 维度 conflict；revert data 弃权/断言；provider code/message 只在 `Decision.Revert`；传输/限流 fault 不进 revert 键。
- Pending：`PendingObserve`（pos 1），pending 结果为 Selected。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`soak 2/0/1 ✅`；revert-data sample 2026-09-23（非普查）。
- Presence：raw WSS 生产路径按 0056.2 保留四态，geth compatibility 仍可能为
  unknown；真实 provider 的 `null` 未观察，WSS provider-backed 四态 golden 仍禁止。

### eth_estimateGas

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`EstimateGas`（支持，typed `uint64`）。
- Typed：`hexutil.Uint64`。
- 错误：median 路径；`ErrNoValidResponse`；typed decode。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1（adapter 固定 `"latest"`）。
- 证据：`soak ✅`；object 形状证据。
- 待办：无新增。

### eth_createAccessList

- Root：`Client.CreateAccessList`（typed `*AccessListResult` + `CallResult`；不进入 `EthClient` 视图，决策 0066）。
- Adapter：无（root-only）。
- Typed：`accesslist.Encode` + root wire 解码。
- 错误：strict/MethodSpecific；`ErrMalformedResponse`。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`eip1898-object 2026-09-23`：latest object 有两个源返回
  `{"accessList":[],"gasUsed":"0x539f"}`，一个源返回空对象，一个源 fault；
  hash/canonical 路径被 pruned-state 拒绝。2026-10-31 latest object live probe
  4/4 返回相同的 `{"accessList":[],"gasUsed":"0x539f"}`；同日历史 block
  18,000,000 的 `blockNumber` object 4/4 返回相同结果；`blockHash` object
  在 `requireCanonical` false/true 两种形式下均 4/4 返回相同结果。
- 待办：hash/canonical 只有单块一致证据，不是跨高度/跨 provider 普查；历史
  object 的可用性仍取决于 provider archive 策略，不得把一次一致样本写成通用
  兼容结论。

## 3. 对象与数组

### eth_getBlockByHash

- Root：`Client.BlockByHash`（typed `*types.Block`）。
- Adapter：`BlockByHash`（支持；专用 hash 方法）。
- Typed：`DecodeBlockWithEvidence`（full）/`DecodeBlockHeaderOnly`；null → `ethereum.NotFound`。
- 错误：admission（header/body/uncles）→ `ErrMalformedResponse`；hash 不匹配 → malformed；null 合法。
- Pending：无（hash 是显式内容）。
- EIP-1898：不适用（hash 参数）。
- Reference：无（hash 读取）。
- 证据：sample 2026-09-23 block 形状（非普查）。
- 待办：历史叔块内容承诺与 provider 兼容性仍待专项验证。

### eth_getBlockByNumber

- Root：`Call`（无专用 root 类型化入口；root `BlockNumber` 是派生，不是本方法）。
- Adapter：`BlockByNumber`（支持；nil/负 tag 先建立参考再按 hash 取块；pending 本地拒绝）。
- Typed：`DecodeBlockWithEvidence`；null → NotFound。
- 错误：header-only/full 形状、admission、请求绑定；hash 位置可能被 provider 拒收。
- Pending：`PendingObserve`（pos 0），pending 结果为 Selected；typed block 不支持 pending。
- EIP-1898：不适用（number/tag 参数）。
- Reference：block wrapper 的 header-only/full result 语义；moving tag 先建立参考。
- 证据：soak 建立每轮 ✅；hash-位置 ⚠（soak 3/4 拒收；2026-10-31 live probe
  为 3 个明确拒收 + 1 个限流，无 provider 接受；同日定向 probe 4/4 明确拒收，
  s1 为 HTTP 400/code 5，其余为 -32602，data 形态含字符串或缺失）。历史
  block 18,000,000 的 header/full 4/4 成功：header 22 个顶层字段、94 笔交易、
  16 个 withdrawals、0 uncles，roots 与 hash 一致；full 交易对象在 3/4 源上
  多出 `blockTimestamp`，1 个源省略该字段。
- 待办：不得把 hash 位置当作通用 provider 能力；uncle 内容仍无真实样本；
  full 交易对象字段差异需要按 typed decoder 的忽略/拒绝规则单独确认。

### eth_getTransactionReceipt

- Root：`Client.TransactionReceipt`（typed `*types.Receipt`）。
- Adapter：`TransactionReceipt`（支持）。
- Typed：`DecodeReceipt`；null → NotFound；tx hash 绑定。
- 错误：strict 对象 admission；hash 不匹配 → malformed。
- Pending：无（hash 读取）。
- EIP-1898：不适用。
- Reference：无。
- 证据：sample 2026-09-23。
- 待办：跨客户端 receipt 字段差异仍按 sample 边界，不是普查。

### eth_getTransactionByHash

- Root：`Client.TransactionByHash`（typed `*types.Transaction`）。
- Adapter：`TransactionByHash`（支持；pending 标志）。
- Typed：`DecodeTransaction`；null → NotFound；unsigned/未知类型 → 明确 typed 错误。
- 错误：admission + `ErrUnsupportedTypedObject`；hash 不匹配 → malformed。
- Pending：无（hash 读取）；adapter 返回 pending 标志。
- EIP-1898：不适用。
- Reference：无。
- 证据：sample 2026-09-23。
- 待办：未知 typed transaction 的 provider 分布未普查。

### eth_getBlockReceipts

- Root：`Client.BlockReceipts`（typed `[]*types.Receipt`）。
- Adapter：`BlockReceipts`（支持；number/tag/hash 形式）。
- Typed：`DecodeReceipts`；null → NotFound；空块 → 空 slice。
- 错误：admission、block hash/height 绑定；pending 本地拒绝。
- Pending：`PendingRefuse`（pos 0）。
- EIP-1898：不支持（adapter 接受 number/hash，不经 EIP-1898 object）。
- Reference：`hash-at` pos 0；number/tag 形式绑定请求高度。
- 证据：sample + soak 2026-09-05 ✅。
- 待办：无新增。

### eth_getLogs

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`FilterLogs`（支持）。
- Typed：`log.Encode` + `types.Log` 解码；null → malformed。
- 错误：range/filter admission；pending 本地拒绝；null 非法。
- Pending：`PendingRefuse`（pos 0）。
- EIP-1898：不适用（filter object）。
- Reference：`height-fields-at` pos 0（`fromBlock`/`toBlock`）。
- 证据：soak 2026-09-05 ✅（70 logs）。
- 待办：大范围 split 与 provider 上限仍按 `range-getLogs split` 记录。

### eth_feeHistory

- Root：`Client.FeeHistory`（typed `*FeeHistory`）。
- Adapter：无（root-only；go-ethereum client 无对应方法）。
- Typed：root wire 解码 + `feehistory.Encode`。
- 错误：range/percentile admission；`ErrConflict`（跨客户端 percentile 舍入）。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：不适用。
- Reference：`height-at` pos 1。
- 证据：`read-blockref 2026-10-21` 的 safe/finalized 捕获中两个源返回相同的
  reward 数组，第三个端点已停止服务；2026-10-31 live probe 中 3/4 在相同的
  `oldestBlock` 返回相同 reward，1 个源的 `oldestBlock` 为不同区间；同日
  固定历史区间（newest block 18,000,000，count 4，percentiles
  `[1,33,67,99]`）4/4 返回相同的 `oldestBlock`、base fee 和 reward 数组；
  `soak ✅`。
- 待办：跨客户端 percentile 舍入仍只有单区间一致证据，未完成多区间/多客户端
  普查；不得用一次一致样本关闭该 liveness gate。

## 4. 兼容性边界

以下边界不新增方法，但每张卡片必须保持诚实：

| 边界 | 当前事实 | 证据/位置 |
| --- | --- | --- |
| revert data | `eth_call` 双维语义；missing/null 弃权，`0x`/具体 data 断言 | 决策 0039/0052/0061；sample 2026-09-23；WSS 2026-10-31 |
| pending | observe vs refuse 由方法声明；typed pending 有明确拒绝 | `moving-tag-matrix.md`；根包 `typed_pending_wire_test.go` |
| EIP-1898 object | balance/nonce/storage/code/call/estimate/createAccessList 支持 | `Specs()` 的 `ReferenceSpec.EIP1898`；2026-10-31 latest object 4/4；历史 block 18,000,000 的 blockNumber object 4/4，balance 的 blockHash object 4/4；createAccessList 的 blockHash object（requireCanonical false/true）4/4；其他高度仍按 provider archive 策略 |
| `eth_blobBaseFee` 支持面 | 方法存在但 provider 支持不一致 | 2026-10-21 sample；2026-10-31 2/4 成功、1 free-plan、1 策略拒绝 |
| `eth_getBlockByNumber` hash 位置 | 不是通用能力 | soak 3/4 拒收；2026-10-31 3 明确拒收 + 1 限流；同日定向 probe 4/4 拒收 |
| 完整区块内容 | 历史 block 18,000,000 header/full 4/4；22 个顶层字段、94 笔交易、16 withdrawals、0 uncles；full 交易对象字段存在 provider 差异 | 2026-10-31 定向 probe；决策 0019 内容承诺仍未完成 uncle 样本验证 |
| NotFound | block/receipt/tx/blockReceipts 的 corroborated null → `ethereum.NotFound` | `internal/typed/decoder.go` |
| 负 block number | 类型化 `-1` 本地拒绝；`-2/-3/-4` 各自建立参考；`-5` 为 earliest | 根包 `typed_block_number_test.go` |
| `requireCanonical` | 不在当前接口；显式 hash/height 绑定由方法 admission 负责 | `spec-baseline.md` 缺口分类 |
| 错误形状 | `ErrConflict`/`ErrNoValidResponse`/`ErrMalformedResponse`/`ErrInvalidRequest`；HTTP/WSS presence 分层 | `rpc/errors.go`、决策 0056 |

## 5. 证据缺口登记

不得用合成样本关闭以下缺口：

1. `eth_blobBaseFee` 的多源支持面：2026-10-21 sample（两源成功、一源 free-plan
   拒绝）；2026-10-31 live probe 为 2/4 成功、1 free-plan、1 策略拒绝；仍没有
   普查或兼容性结论；
2. `eth_syncing` progress object 的真实 provider 形状（T09 capture gap）；
3. EIP-1898 object 的历史状态可用性：2026-10-31 latest object 已 4/4 成功，
   同一日 block 18,000,000 的 blockNumber object 4/4、balance blockHash object
   4/4；2026-09-23 的历史 object 只有单源成功（两个 pruned、一个空），说明
   provider archive 策略仍会改变可用性；不能升级为“任意历史高度均支持”；
4. `eth_getBlockByNumber` 的 hash 位置在真实 provider 上的接受度
   （soak 3/4 拒收；2026-10-31 3 明确拒收 + 1 限流，同日定向 probe 4/4 拒收，
   不能升级为通用能力）；
5. `eth_createAccessList` 的 hash/canonical 形式：latest 与历史 block
   18,000,000 的 object 形式已 4/4 一致，`blockHash` 的
   `requireCanonical` false/true 也 4/4；仍只有单块证据，不是跨高度普查；
6. `eth_feeHistory` 的跨客户端 percentile 舍入（liveness gate；2026-10-31
   3/4 同一 `oldestBlock` 一致，1 个源区间不同；固定历史区间
   `[1,33,67,99]` 4/4 一致，但仍只有单区间证据）；
7. WSS error.data 的真实 provider 证据：missing/`0x`/value 已观察，`null`
   未观察；0056.2 批准生产保真，不关闭 W4/provider-backed golden 证据门；
8. 完整区块内容承诺（uncle/withdrawals/交易根）的真实多源兼容性
   （2026-10-31 历史 block 18,000,000 header/full 4/4，16 withdrawals、
   0 uncles；uncle 样本和 full 交易对象字段差异仍未验证）。

## 6. 维护规则

- 新增/变更方法行为先更新 `resolver-behavior-inventory.md` 或本卡片，再改
  生产代码；
- provider 证据只能登记 capture/replay/live 的日期与等级，不能用离线测试
  或合成样本替代；
- 卡片中的“支持/root-only”状态以 `ethguard.Client` 公开方法为准；新增公开方法
  方法需要独立切片；
- 本文件不改变 Decision、quorum、reference、typed 或方法数量。

## 7. 2026-10-31 方法 live probe

这是一次手动、只读、仓库外的 live observation，不是 capture batch，也没有
写成 expect/golden：

- 4 个公开 HTTPS endpoint（无需凭据），每个 10 个请求，共 40 条；
- 请求：EIP-1898 latest object 的 balance/nonce/storage/code、
  createAccessList；`eth_getBlockByNumber` 的 hash-position object；
  `eth_syncing`；`eth_blobBaseFee`；`eth_feeHistory`；latest block；
- 结果：
  - latest object 的 balance/nonce/storage/code 4/4 成功；
  - createAccessList latest object 4/4 返回相同
    `{"accessList":[],"gasUsed":"0x539f"}`；
  - hash-position 3 个明确拒收 + 1 个限流，无接受；
  - blobBaseFee 2/4 成功、1 free-plan、1 策略拒绝；
  - feeHistory 3/4 在相同 `oldestBlock` 一致，1 个源区间不同；
  - latest block 4/4 同一 26-key 顶层集合，3/4 同一 block，1 个落后；
  - syncing 4/4 为 `false`；
- 该 probe 只补充 live observation，不替代历史 capture/replay，也不把
  单次一致样本升级为长期兼容性结论；endpoint 和原始记录不进入仓库。

## 8. 2026-10-31 定向历史块 probe

这是同日另一次手动、只读、仓库外的 live observation，使用
`cmd/rpcsample` 记录 redacted wire 记录，不写成 expect/golden：

- 4 个公开 HTTPS endpoint（无需凭据），每个 11 个请求，共 44 条；
- 固定历史块：mainnet block 18,000,000（`0x112A880`），hash
  `0x95b198e154acbfc64109dfd22d8224fe927fd8dfdedfae01587674482ba4baf3`；
- 请求：balance 的 `{blockNumber}` 与 `{blockHash}` object、
  nonce/storage/code/createAccessList 的 `{blockNumber}` object、
  `eth_feeHistory [4, 0x112A880, [1,33,67,99]]`、历史 block 的 header/full、
  hash-position object、`eth_syncing`；
- 结果：
  - balance 的两种 EIP-1898 object 形式均 4/4 成功且值一致；
  - nonce/storage/code/createAccessList 的历史 `{blockNumber}` object 均 4/4
    成功且值一致；createAccessList 全部返回
    `{"accessList":[],"gasUsed":"0x539f"}`；
  - createAccessList 的 `{blockHash}` object 在 `requireCanonical` false/true
    两种形式下均 4/4 返回相同结果；同日 balance 的 `blockHash` +
    `requireCanonical:true` captured run 为 3/4，1 个源返回 HTTP 499 空响应
    （同日直接 probe 为 4/4）；
  - feeHistory 固定区间 4/4 返回相同 `oldestBlock`、base fee 和 reward 数组；
  - 历史 block header 4/4 返回相同 hash、22 个顶层字段、94 笔交易、16 个
    withdrawals、0 uncles 和相同 roots；full block 4/4 的交易数与首尾 hash
    一致，但 1 个源的交易对象省略 `blockTimestamp`；
  - hash-position 4/4 拒绝：1 个源 HTTP 400/code 5，其余为 -32602；
  - syncing 4/4 为 `false`；
- 该 probe 只证明这些 provider 在这个历史块上的可用形状，不是历史 archive
  普查，也不是长期兼容性结论；endpoint 和原始记录不进入仓库。

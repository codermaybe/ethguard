# 方法对接卡片（23 个 RegisteredMethod）

状态：**卡片已建立（2026-10-31）**。本文件按业务语义为每个已注册
read 方法维护一张对接卡片，记录 root、ethadapter、typed、错误、pending、
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
- typed 边界要求 Corroborated（`refcontext.RequireCorroborated`）；
  Selected 不静默替代；
- 固定分母、quorum、reference、EIP-1898 改写和 pending 语义由 ReadPlan
  声明，卡片只登记现状，不重新定义。

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
  一个源返回 free-plan 错误；公开 capture 与 replay case 已归档；不是普查，也
  不是通用兼容结论）。
- 待办：该错误证明 provider 支持面存在差异；不得按方法存在即宣称兼容。

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
- 证据：`soak census ✅`；`read-volatile 2026-10-21` 三个源均返回 `false`，未覆盖
  progress object。progress object 形状仍是 capture gap，T09 未合成。
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
- 证据：`soak 2026-09-05 ✅`；EIP-1898 object 形状证据。
- 待办：无新增。

### eth_getTransactionCount

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`NonceAt`（支持，typed `uint64`）。
- Typed：`hexutil.Uint64`。
- 错误：strict 路径；typed decode。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`soak ✅`；`eip1898-object 2026-09-23` 已归档 `{"blockNumber":...}` 请求，
  仅一个源成功返回 quantity，两个源报告 pruned state，一个源为空；多源 object
  形状仍未确认。
- 待办：不得把单源成功写成通用 EIP-1898 object 支持；replay 未达到 corroboration。

### eth_getStorageAt

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`StorageAt`（支持，typed `[]byte`）。
- Typed：`EncodeStorageWord` 对应的 typed 解码。
- 错误：strict 路径；malformed word → `ErrMalformedResponse`。
- Pending：`PendingObserve`（pos 2）。
- EIP-1898：支持。
- Reference：`hash-at` pos 2。
- 证据：`soak ✅`；`eip1898-object 2026-09-23` 的 `{"blockNumber":...}` 请求只有
  一个源成功返回 storage word，两个源报告 pruned state，一个源为空；多源 object
  形状仍未确认。
- 待办：不得把单源成功写成通用 EIP-1898 object 支持；replay 未达到 corroboration。

### eth_getCode

- Root：`Call`（无专用 root 类型化入口）。
- Adapter：`CodeAt`（支持，typed `[]byte`）。
- Typed：`EncodeData` 对应的 typed 解码。
- 错误：strict 路径；malformed data → `ErrMalformedResponse`。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`soak ✅`；`eip1898-object 2026-09-23` 的 `{"blockHash":...}` 请求只有
  一个源成功返回 code，两个源报告 pruned state，一个源为空；多源 object 形状
  仍未确认。
- 待办：不得把单源成功写成通用 EIP-1898 object 支持；replay 未达到 corroboration。

### eth_call

- Root：`Client.Call`（通用；返回 Decision，revert 由 adapter 解码）。
- Adapter：`CallContract`（支持；`RevertError`）。
- Typed：`decodeCallDecision` → success DATA / `RevertError`。
- 错误：execution 维度 conflict；revert data 弃权/断言；provider code/message 只在 `Decision.Revert`；传输/限流 fault 不进 revert 键。
- Pending：`PendingObserve`（pos 1），pending 结果为 Selected。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`soak 2/0/1 ✅`；revert-data sample 2026-09-23（非普查）。
- 待办：WSS 路径的 missing/null 仍为 unknown（决策 0056）；2026-10-31 的真实
  WSS 证据观察到 missing/`0x`/value，`null` 未观察，因此仍不能升级四态断言。

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

- Root：`Client.CreateAccessList`（typed `*AccessListResult`）。
- Adapter：无（root-only）。
- Typed：`accesslist.Encode` + root wire 解码。
- 错误：strict/MethodSpecific；`ErrMalformedResponse`。
- Pending：`PendingObserve`（pos 1）。
- EIP-1898：支持。
- Reference：`hash-at` pos 1。
- 证据：`eip1898-object 2026-09-23`：latest object 有两个源返回
  `{"accessList":[],"gasUsed":"0x539f"}`，一个源返回空对象，一个源 fault；
  hash/canonical 路径被 pruned-state 拒绝。object 形状证据存在，但不是多源
  兼容结论。
- 待办：不得以样本关闭 provider 兼容性缺口；hash/canonical 形式仍待真实 capture。

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
- 证据：soak 建立每轮 ✅；hash-位置 ⚠（3/4 拒收）。
- 待办：不得把 hash 位置当作通用 provider 能力。

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
  reward 数组，第三个端点已停止服务；`soak ✅`。跨客户端 Reward 舍入仍是
  liveness gate。
- 待办：percentile 舍入的 provider 证据未普查；不得用一次一致样本关掉舍入缺口。

## 4. 兼容性边界

以下边界不新增方法，但每张卡片必须保持诚实：

| 边界 | 当前事实 | 证据/位置 |
| --- | --- | --- |
| revert data | `eth_call` 双维语义；missing/null 弃权，`0x`/具体 data 断言 | 决策 0039/0052/0061；sample 2026-09-23 |
| pending | observe vs refuse 由方法声明；typed pending 有明确拒绝 | `moving-tag-matrix.md`；`ethadapter/pending_wire_test.go` |
| EIP-1898 object | balance/nonce/storage/code/call/estimate/createAccessList 支持 | `Specs()` 的 `ReferenceSpec.EIP1898`；nonce/storage/code 仅单源成功（2026-09-23），多源形状未确认 |
| NotFound | block/receipt/tx/blockReceipts 的 corroborated null → `ethereum.NotFound` | `internal/typed/decoder.go` |
| 负 block number | adapter `-1` 拒绝；`-2/-3/-4` 各自建立参考；`-5` 为 earliest | `ethadapter/block_number_negative_test.go` |
| `requireCanonical` | 不在当前接口；显式 hash/height 绑定由方法 admission 负责 | `spec-baseline.md` 缺口分类 |
| 错误形状 | `ErrConflict`/`ErrNoValidResponse`/`ErrMalformedResponse`/`ErrInvalidRequest`；HTTP/WSS presence 分层 | `rpc/errors.go`、决策 0056 |

## 5. 证据缺口登记

不得用合成样本关闭以下缺口：

1. `eth_blobBaseFee` 的多源支持面：已有 2026-10-21 sample（两源成功、一源
   free-plan 拒绝），但没有普查或兼容性结论；
2. `eth_syncing` progress object 的真实 provider 形状（T09 capture gap）；
3. `eth_getTransactionCount`、`eth_getStorageAt`、`eth_getCode` 的 EIP-1898
   object 多源形状：2026-09-23 仅一个源成功，两个源 pruned state、一个源为空；
   不能升级为通用能力；
4. `eth_getBlockByNumber` 的 hash 位置在真实 provider 上的接受度
   （soak 3/4 拒收，不能升级为通用能力）；
5. `eth_createAccessList` 的真实 provider 兼容性（仅 object 形状证据）；
6. `eth_feeHistory` 的跨客户端 percentile 舍入（liveness gate）；
7. WSS 路径的 error.data presence 与真实 provider 行为（决策 0056 未修订）；
8. 完整区块内容承诺（uncle/withdrawals/交易根）的真实多源兼容性
   （决策 0019 的验证仍未完成）。

## 6. 维护规则

- 新增/变更方法行为先更新 `resolver-behavior-inventory.md` 或本卡片，再改
  生产代码；
- provider 证据只能登记 capture/replay/live 的日期与等级，不能用离线测试
  或合成样本替代；
- 卡片中的“支持/root-only”状态以 `ethadapter` 公开方法为准；新增 adapter
  方法需要独立切片；
- 本文件不改变 Decision、quorum、reference、typed 或方法数量。

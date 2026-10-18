# Capture golden 契约（读方法捕获批次）

本文件冻结「未捕获读方法」进入正式 capture 批次前必须先确定的形。它不是
路线图，是**执行前的契约**：一次冻结，之后所有分组捕获按它生成 expect，
不得在捕获过程中反复改结构。规则的权威实现仍是测试；本文件只作对照与
说明书。

适用范围：`project-roadmap.md`「排后：未捕获读方法的捕获批次」列出的 11 个
读方法，以及未来任何新捕获批次。

---

## 1. 传输：只捕获 HTTP

**契约 G1：每个 `expect.json` 的 `transport` 必须是 `"http"`。**

- 已由 `validateExpectHeader` 强制（`internal/rpcsample/replay/expect_test.go`）：
  非 `http` 直接报错，并给出理由「on WSS a missing data field collapses into null」。
- **WSS golden 继续拒绝**，理由归于决策 0056/0056.1。本文件不重复该决策，
  只声明：捕获批次**不生成、不接受、不预留** WSS 变体。

### 1.1 WSS 是否阻塞本批 11 个方法

**不阻塞。** 判断依据（逐方法核实）：

- 被 WSS presence 坍缩影响的路径是 **JSON-RPC `error` body 进入裁决**，
  当前唯一这样的核心方法是 `eth_call`（`WithRPCErrorBodies()`），**不在本批**。
- 本批 11 个方法（`eth_getLogs`、`eth_feeHistory`、`eth_chainId`、`net_version`、
  `eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_blockNumber`、
  `eth_syncing`、`net_peerCount`、`net_listening`）的正常路径返回 `result`；
  其失败多为非 revert 协议错误或传输错误，**不进入 revert 比较键**。
- 因此对它们而言，`missing` 与 `null` 的坍缩**没有裁决影响**，HTTP 捕获的
  golden 完全充分。

**结论**：本批按 `transport: "http"` 捕获即可；WSS 缺口不构成先决条件。
将来若产品需要 WSS 四态 golden，那是决策 0056 升级之后的事，**不回溯本批**。

---

## 2. anchor 规则：按方法分类，不虚构

**契约 G2：只有带 block-reference 参数的方法才要求 `safe`/`finalized` 各自锚定。**

| 类别 | 本批方法 | anchor 要求 |
| --- | --- | --- |
| block-reference | `eth_getLogs`、`eth_feeHistory` | **必须** `safe`/`finalized` 各自锚定；**不得**落 `latest` |
| 无参数 / chain-local / variable | `eth_chainId`、`net_version`、`eth_blockNumber`、`net_peerCount`、`net_listening`、`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_syncing` | **不虚构 anchor**；按方法自身形态捕获 |

- 依据：moving-tag 只对含 block-reference 参数的方法有意义；对 chain/local
  方法强加 anchor 会制造一个不存在的一致性维度。
- 实现参考：`replayRegistry` 已按此区分装配 —— `LogsPolicy(height)` /
  `FeeHistoryPolicy(height)` 接收 height 解析器，其余为无参 policy
  （`internal/rpcsample/replay/layers_test.go`）。

### 2.1 上游不支持 `safe`/`finalized` 时的备选

`safe`/`finalized` 是执行层 tag，不是所有链/节点都支持（L2、测试网、归档不足
的节点可能拒绝或返回 `null`）。处理纪律：

- **换源**，而不是回落 `latest`；
- 若所有可用源都不支持，则该 case **登记为未覆盖**，**不得伪造 record**；
- 绝不把 `latest` 当作 `safe`/`finalized` 的替代，也不降 anchor 要求。

该问题只影响组 3（`eth_getLogs`/`eth_feeHistory`），不阻塞其余 9 个方法。

### 2.1bis 真实捕获的锚定上下文：载体与恢复（已选定）

`cmd/rpcsample` 直接按 plan 向各源发请求，**不建立 EthGuard 的共同 reference**。
直接向不同源发 `"safe"` 可能录到不同高度的合法响应；replay 的模拟高度不能用
来验证真实主网日志。因此组 3 必须先定型上下文。

**载体（已选定）**：

- **原始 record 不变。** record 里的 `request` 永远是**实际发出的 wire 请求**。
- **`batch.json` 新增可选字段 `reference_context`，按业务 `request_id` 索引**。
- **不扩展 `expect/v1` schema**：`expectCase.Request` 继续表示实际 wire 请求；
  逻辑（prepared 前）请求放在批次上下文，由测试检查两者的关系。

> **为什么是 `expect/v1` 不扩、而 `batch.json` 要加**：两者的兼容性选择不同。
> 不扩 `expect/v1` 是**保持契约兼容**的选择（现有 expect 已定型；避免在一次
> 捕获里再引入第二套 JSON 形态）；而 `batchMeta` 读的是 `batch.json`，本轮
> 恰好需要给它**新增一个可选字段**，这是允许的。`DisallowUnknownFields` 是在
> 另一端需要**同步维护**的实现约束（新增字段要同时改读取结构），不是
> 「`batch.json` 也不能扩展」的理由。

**`reference_context` 最小字段**（每项按业务 `request_id` 索引）：

| 字段 | 含义 |
| --- | --- |
| `logical_request` | 锚定前的 `method`、`params`（原始逻辑请求） |
| `tag` | `safe` 或 `finalized` |
| `established_at` | 捕获时 reference 的建立时间 |
| `members` | **有序**来源 ID，包含失败成员 |
| `reference_records` | 建立 reference 所需的实际请求/响应记录引用：关联来源 ID，并经过 **SHA-256 校验** |
| `expected_reference` | 捕获时得到的 `height`、`hash`、`status`、`support` **（离线重算的预期结果）** |

**关键区别（不得混淆）**：

- `reference_records` 是**回放输入**；
- `expected_reference` 是**重算后的断言目标**，**不得直接注入 resolver**；
- `members` 只有来源名单还不够，必须能**定位实际 reference 响应**；若建立
  reference 有后续确认请求，也要保留。

**离线恢复（不是只读一个手填高度）**：

1. 从上下文恢复逻辑请求；
2. 用**捕获的 reference 响应**驱动真实 anchor 路径（而不是注入数值高度）；
3. 断言发出的业务请求 **等于 record 中的实际请求**。

**硬约束**：

- **record 保留实际发出的请求**，**不得事后改写成「仿佛经过锚定」**。
- `expect` 的 `request` 字段继续表示**实际 wire 请求**；逻辑请求放批次上下文，
  由测试检查两者关系，**暂不扩展 expect schema**。
- **两层使用相同成员顺序和现有 mode/floor 快照**，不另建第二套 quorum 配置。
- **reference 记录与业务记录明确区分**：不能让通用分组器把辅助 reference
  请求误当业务 golden case。
- prepared request 与 record 比较 **method 和 params**；JSON-RPC `id` 是传输
  关联标识，**不要求**离线生成的 ID 与录制 ID 相同。
- **历史三批可以没有 `reference_context`**；**新的组 3 case 必须有**，缺失或
  引用无效则**失败**。
- `batch.json` 的读取结构与历史批次检查需兼容新上下文（新增**可选**字段）。

---

### 2.2 `eth_feeHistory` / `eth_getLogs` 的 anchor 语义（已确定）

- 请求参数为 `[blockCount, newestBlock, rewardPercentiles]`；block-reference
  **只出现在参数位置 1 的 `newestBlock`**，`blockCount` 与
  `rewardPercentiles` 不是 block-reference。
- `FeeHistoryPolicy(height)` 的语义**已确定**：参数 1 的移动标签重写为
  **reference 高度**，**不是 hash**。依据 `rpc/methods/eth_feahistory.go`：
  `feeHistory` 的 `newestBlock` 是 `BlockNumber`，32 字节 hash 上不了 wire。
- 因此 expect 断言的是「移动标签被改写为 corroborated reference 的高度」，
  **不得**断言为 hash 改写。
- `eth_getLogs` 同理：范围在过滤对象的 `fromBlock`/`toBlock`，也是**高度改写**
  （`rpc/methods/eth_getlogs.go` 的 `WithAnchorHeightFields`）。

---

## 3. golden 断言什么：按方法声明，不统一

**契约 G3：每个 golden 必须按其 resolver 声明断言类型，不得把不同类型混为
一谈。**

允许的断言类型（在 expect 里通过对应用例表达，不在本文件引入新 schema）：

- `wire/shape`：请求/响应形状、字段存在性与形态（例如 `eth_syncing` 的
  `false` vs 对象；`eth_getLogs` 的空数组 vs 日志对象）。
- `Corroborated`（仅参与佐证的方法）：`eth_chainId`、`net_version`、
  `eth_getLogs`、`eth_feeHistory`。
- `Selected/local`：`eth_gasPrice`、`eth_maxPriorityFeePerGas`、
  `eth_blobBaseFee`、`eth_blockNumber`（lower median）与 `eth_syncing`、
  `net_peerCount`、`net_listening`（local）。不得升为 `corroborated`。
- `Fault/Forwarded` 状态：错误分类、转发结果。

**禁止**：

- 把「跨批次数值相等」当成契约。gas、tip、blob fee、block number 随时间变化，
  它们的 golden 断言的是**冻结 record 的回放一致性**，不是数值本身。
- 把 local/median 的结果写成佐证结论；median 的 spread evidence 是诊断，
  不是结论。
- 把 `Selected`/`Forwarded` 冒充 `Corroborated`。

---

## 4. 捕获产物的固定形状

每个方法的捕获必须产出（与现有批次一致）：

1. `testdata/<feature>/<batch-id>/batch.json`（`ethguard.batch/v1`，含
   `feature`、`batch_id`、`network`、`captured_at`）——**feature 在此处体现**；
2. `testdata/<feature>/<batch-id>/index.json`（含每 record 的 `sha256`；
   **index.json 没有 `feature` 字段**）；
3. `records/<request-id>--<source-id>.json`（冻结的 wire 响应）；
4. `expect.json`（`schema_version: ethguard.expect/v1`，`transport: http`；
   含 `feature` 字段）；
5. `noCapture` 清单更新：从 `internal/rpcsample/replay/batch_expect_test.go`
   的 `noCapture` map 中移除该方法；
6. 批次 ID 随批次更新。

feature 的权威落点是 **目录名 + `batch.json` + `expect.json`**（三者必须一致，
`validateExpectHeader` 校验 `expect.json` 与目录名；`replay_test.go` 校验
`batch.json`）。

### 4.0 feature 是什么

`feature` 是 **EthGuard 自己的捕获批次分组标签**，不是官方标准：

- 它的权威落点是 **目录名 + `batch.json` + `expect.json`**（三者必须一致）；
  `index.json` **没有** `feature` 字段；
- `validateExpectHeader` 校验 `expect.json` 的 `feature` 等于目录名，
  **无官方枚举约束**；`replay_test.go` 校验 `batch.json` 的 `feature`；
- 现有 feature：`revert-data`、`block-receipt-tx`、`eip1898-object`；
- 新增 feature 后，**必须随该批次落地**把新 feature 名接入：
  - `TestRegisteredMethodSampleStatus` 的遍历列表（`batch_expect_test.go`）；
  - 其它遍历三个旧 feature 的测试入口（`replay_test.go` 等）；
  - 该批次的 **expect 回放测试入口**（否则 expect 永远不会被跑）；
  - feature 与 batch-id 的对应关系（不能改全局常量 `sampleBatchID`，那会
    影响历史批次）。

> **不提前占位**：若在阶段 0 就把尚不存在的新目录加入强制加载列表，阶段 1 会
> 无法保持测试全绿。新 feature 的接入应**随各批次落地**，不能先行。

> **互锁不充分**：`TestRegisteredMethodSampleStatus` 只检查 record 中出现的
> 方法，不证明该方法有 expect，也不证明 expect 被运行过。必须同时接通 expect
> 回放入口（见上），它只是必要的第一步，不是充分条件。

**契约 G4：replay 离线。**

- 允许进程内 `httptest`；**禁止外部网络 I/O**。
- 捕获阶段（`cmd/rpcsample` + plan）可以在受控环境进行一次真实录制，
  但**提交进仓库的只能是冻结的 record**；测试运行不得触网。
- 不得盲目 `-update`；每次 expect 变更必须能解释为对冻结 record 的回放。

---

**契约 G5：参与佐证的方法，`n` = 该次调用在 Aggregate 入口捕获的投票成员数（voting basis）。**

- 成员集合在 `Aggregate` 入口、`Prepare` 与响应准入**之前**捕获；不是响应
  「准入时」才确定。每个成员都计入分母，包括响应无效或被 gate 跳过的成员；
  分母**不因**「成功源数少」而缩小。
- `expect.n` 必须等于该 case 的 `len(sources)`（`validateExpectHeader` 硬校验
  `item.N != len(item.Sources)`）。这是**记录完整性约束**，不是所有方法都有
  同样的成功条件。
- 门槛 `Q = max(H, floor(n/2)+1)`，其中 `H` 是 `floor`（`rpc/adjudicate.go`
  的 `Corroboration.Required`）。该公式**只适用于参与佐证的方法**。

### 5.1 按 resolver 区分的成功条件（不统一套用 quorum）

| 方法 | 成功状态 / 规则 |
| --- | --- |
| `eth_chainId`、`net_version`、`eth_getLogs`、`eth_feeHistory` | 达门槛后 `Corroborated` |
| `eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_blockNumber` | lower median，`Selected` |
| `eth_syncing`、`net_peerCount`、`net_listening` | local 观察，`Selected` |

- **local/median 不适用上述 quorum 成功条件**；它们的 golden 断言的是
  `Selected`/local 语义与 record 回放一致性，**不得**写成 `Corroborated` 结论。
- median 的 spread evidence 是**诊断**，不得当成佐证结论。
- 达不到 quorum 时保持冲突结论（`Unknown`/`Conflict`），**不降 floor、不缩
  分母**（`balance-safe-canonical` 的教训即此）。

「录制几个源」只是产生该 case 分母的手段；分母的**语义**始终是 Aggregate 入口冻结的成员数，
不是「成功返回值的源数」。

---

## 6. 完成定义

「完成」分两级，不得只凭 `noCapture` 清空就宣称全部完成：

**L1：方法已有真实归档，且回放通过**

- [ ] 该方法至少有一个真实捕获的 batch（`batch.json` + `index.json` +
  `records/` + `expect.json`）；
- [ ] 该方法已从 `noCapture` map 移除；
- [ ] `TestRegisteredMethodSampleStatus` 通过（只证明「有归档」）；
- [ ] 该方法的 **expect 回放测试已接入并通过**（这是 L1 的必要部分，
  `TestRegisteredMethodSampleStatus` 不承担此职）。

**L2：必需 case 已全部覆盖（需专门的覆盖断言）**

- [ ] 该方法的必需 case 全部有 record 与 expect，且**由覆盖断言实际检查**，
  不能只检查 request-id 名称。至少包括：
  - `eth_syncing`：`false` 与对象态；
  - `eth_getLogs`：空结果与非空结果，及 `safe`/`finalized` 两个 tag 的上下文；
  - `eth_feeHistory`：`safe`/`finalized` 两个 tag 的上下文；
- [ ] 未覆盖项**如实登记**，不得用假 record 填。

> `noCapture` 清空只能证明 L1。缺少必需 case 时可以提交部分成果（L1 完成），
> 但**L2 保持未完成**，且**不得以跳过测试制造完成**。

**批次级完成**（L2 全部满足）额外要求：

- [ ] `matchCapturedExpect` / 回放测试全绿；
- [ ] 全量 `go test ./... -count=1` 无跳过；
- [ ] 未降低任何门槛、未删投票分母、未改动品牌字符串。

---

## 7. 与 WSS 的关系（一次性结论）

- 本批捕获**不生成** WSS golden；WSS 四态 golden 由决策 0056 拒绝。
- WSS presence 缺口**不阻塞**本批（见 §1.1）：本批默认 policy **未开启
  `WithRPCErrorBodies()`**，missing/null 不会成为业务投票内容。
- 但这只能说明 **HTTP golden 足以推进本批**；它**不证明** WSS 传输行为已经
  验收。WSS 传输验收仍是决策 0056 升级之后的事。
- 若未来决策 0056 升级、WSS 四态保真落地，**本批 golden 不需重做**——
  它们断言的是 HTTP 路径下的裁决，与传输保真无关。

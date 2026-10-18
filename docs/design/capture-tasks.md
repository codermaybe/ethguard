# EthGuard 读方法捕获任务清单

> **本文件是派发副本，不是持久计划。** 进度与状态只在
> `docs/design/project-roadmap.md` 维护；本文件随任务派发，修订后重新派发。
> 契约规则以 `docs/design/capture-golden-contract.md`（G1–G5）为准。

**交付对象**：glm5.3flash
**前置契约**：`docs/design/capture-golden-contract.md`。执行前先通读。
**适用方法**：`project-roadmap.md`「排后：未捕获读方法的捕获批次」列出的 11 个读方法。

---

## 前置结论（勿再反复）

1. **WSS presence 缺口不阻塞本批**（契约 §1.1/§7）。本批默认 policy **未开启
   `WithRPCErrorBodies()`**，missing/null 不会成为业务投票内容。全部按
   `transport: "http"` 捕获，不生成 WSS 变体。**这不证明 WSS 传输已验收。**
2. **分母 `n`**：参与佐证的方法，`n` = **Aggregate 入口捕获的投票成员数**
   （不是「准入时」才确定，也不是成功源数）；`n == len(sources)` 是记录完整性
   约束。quorum 公式**只适用于参与佐证的方法**（契约 §5、§5.1）。
3. **成功条件按 resolver 区分**（契约 §5.1）：
   - `Corroborated`：`eth_chainId`、`net_version`、`eth_getLogs`、`eth_feeHistory`
   - lower median / `Selected`：`eth_gasPrice`、`eth_maxPriorityFeePerGas`、
     `eth_blobBaseFee`、`eth_blockNumber`
   - local / `Selected`：`eth_syncing`、`net_peerCount`、`net_listening`
4. **feature 是自建批次标签，非官方标准**（契约 §4.0）。
5. **`safe`/`finalized` 不支持时换源或登记未覆盖，不得回落 `latest`**（契约 §2.1）。
6. **测试运行禁止外部网络 I/O**；真实录制只在捕获阶段一次（契约 G4）。

---

## 仓库已有基础设施（必须复用，禁止重写）

- `cmd/rpcsample`：按 plan 录制 → `index.json`（含 sha256）+ `records/`。
  **它不生成 `batch.json`**；batch 元数据由捕获流程**另行产出并校验**
  （现有三批的 `batch.json` 不是 `cmd/rpcsample` 写的）。
- `replayCapturedCase` / `replayRegistry` / `matchCapturedExpect`：回放驱动与断言。
- `validateExpectHeader`：强制 `transport: "http"`。
- `TestRegisteredMethodSampleStatus` + `noCapture` map：样本状态互锁
  （**只证明「有归档」，不证明有 expect，也不证明 expect 被跑过**）。

---

## 阶段 0：上下文定型 + replay 补齐（先做，阻塞组 3）

> **顺序：T0.1 → T0.2（上下文载体）→ T0.3（接入 resolver）→ T0.4（断言）**。
> 必须先定载体，再接 resolver，否则会把「另一套测试高度」先写死。

- [ ] **T0.1** 通读 `docs/design/capture-golden-contract.md`（重点 §2.1、§2.1bis、§2.2、§4.0、§5.1、§6）。
- [ ] **T0.2 确定锚定上下文载体与恢复契约**（契约 §2.1bis，已选定字段）：
  - 原始 record 不变；`batch.json` **新增可选 `reference_context`**，按业务
    `request_id` 索引；
  - 不扩展 `expect/v1`（保持契约兼容的选择；`DisallowUnknownFields` 是需同步
    维护的实现约束）；
  - 每项字段：`logical_request`、`tag`、`established_at`、`members`（有序，含
    失败成员）、`reference_records`（关联来源 ID + SHA-256 校验）、
    `expected_reference`（height/hash/status/support）；
  - `reference_records` 是回放输入；`expected_reference` 是重算后的断言目标，
    **不得直接注入 resolver**；
  - 离线恢复：从上下文恢复逻辑请求 → 用**捕获的 reference 响应**驱动真实
    anchor 路径 → 断言发出的业务请求等于 record 请求；
  - `batch.json` 读取结构与历史批次检查需兼容：历史三批可无此字段；**新组 3
    case 必须有，缺失或引用无效即失败**。
    （glm 可自行设计内部 Go 类型、辅助函数与文件组织；本项只固定捕获产物契约。）
- [ ] **T0.3 接入 height resolver 并统一两层**（核心阻塞项）：
  - 现状：`replayCapturedCase`（`internal/rpcsample/replay/batch_expect_test.go`）
    调用 `replayRegistry(taggedAnchor, nil)`，**height resolver 传 nil**，而
    Aggregator 层有 resolver，两层无法对齐。
  - 动作：为 replay 的 Policy 层接入与 Aggregator **同一份批次 reference 上下文**，
    使 logs/feeHistory 的移动标签在两层都解析到同一 reference 高度。
- [ ] **T0.4 补齐可观察验收断言**（不预设新函数名，但要能看到以下结果）：
  - **同一上下文**：Policy 与 Aggregator 使用同一份批次 reference 上下文；
  - **prepared 一致**：两层 prepared request 一致，**且等于 record 中实际发出的请求**；
  - **高度改写**：logs 的 `fromBlock`/`toBlock`、feeHistory 的参数 1 都是对应的
    reference 高度（不是裸 tag）；
  - **未落 latest**：reference 查询保留原始 `safe`/`finalized`，没有落到 `latest`；
  - **两层对齐**：状态、Value、Support/Reject、Faults 对齐；
  - **两层同源配置**：使用相同成员顺序和现有 mode/floor 快照，**不另建第二套
    quorum 配置**；
  - **记录区分**：reference 记录与业务记录分开，通用分组器不得把辅助 reference
    请求当成业务 golden case；
  - **prepared 比较口径**：比较 `method` 与 `params`；JSON-RPC `id` 是传输关联
    标识，不要求离线生成的 ID 与录制 ID 相同；
  - **不匹配即失败**：reference 上下文与 record 高度不匹配时，**回放必须失败**。
- [ ] **T0.5 记录当前 `noCapture` map 的 11 项**，作为勾选基准。
- [ ] **T0.6 准备 plan**（`ethguard.rpc-sample-plan/v1`）。
- [ ] **T0.7 网络上下文**：现有 replay 节点与 pool **固定 chain ID 为 1**。若捕获
  非主网链，需要**独立批次与网络上下文**；**不得跨链混成一次投票**。

---

## 阶段 1：组 1 — 参与佐证 chain/local（无 anchor）

**方法**：`eth_chainId`、`net_version`、`eth_blockNumber`、`net_peerCount`、`net_listening`
**feature**：`read-local`（名称待定，须与目录名/batch.json/expect.json 一致）

- [ ] **T1.1 plan**：`eth_chainId`/`net_version`/`eth_blockNumber`/`net_peerCount`/`net_listening` 均无参数。
- [ ] **T1.2 捕获**：`cmd/rpcsample` 录制。录制后人工**检查脱敏、截断与记录
  完整性**（检查手段：`RecordedError`、`Truncated` 字段、sha256 一致性），
  **不是**「删除不利响应」。
- [ ] **T1.3 expect**：`schema_version: ethguard.expect/v1`、`transport: "http"`；
  `n == len(sources)`；`projection.deleted_fields` 与 `dimensions` 齐全。
- [ ] **T1.4 断言类型按方法声明**（契约 §3、§5.1）：
  - `eth_chainId`、`net_version` → `Corroborated`（达门槛后）；
  - `eth_blockNumber` → lower median / `Selected`；
  - `net_peerCount`、`net_listening` → local / `Selected`。
  **不得**统一写成「跨源结果」，**不得**把 median/local 写成佐证。
- [ ] **T1.5 接入互锁与回放入口**（随本批落地，不提前占位）：
  - 从 `noCapture` map 移除这 5 个方法；
  - 把新 feature 接入：
    - `TestRegisteredMethodSampleStatus` 的遍历列表；
    - 其它遍历旧 feature 的测试入口；
    - **该批次的 expect 回放测试入口**（否则 expect 永远不会被跑）；
    - feature 与 batch-id 的对应关系（**不能改全局 `sampleBatchID` 常量**，
      那会影响历史批次）；
  - 产出 `batch.json`（`ethguard.batch/v1`）。
- [ ] **T1.6 验收**：`go test ./internal/rpcsample/... -count=1` 全绿；
  `TestRegisteredMethodSampleStatus` 通过；`go test ./... -count=1` 无跳过。
- [ ] **T1.7 提交**：独立 commit。

---

## 阶段 2：组 2 — median/local 可变数值（无 anchor）

**方法**：`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_syncing`
**feature**：`read-volatile`（名称待定）

- [ ] **T2.1 plan + 捕获**：同 T1.2 流程。
  - **`eth_syncing` 必须覆盖两态**：`false` 与对象（同步中）。
  - **对象态不能靠来源数量保证取得**；找不到就**如实登记覆盖缺口**，
    **禁止合成 provider record**。
- [ ] **T2.2 expect**：`transport: "http"`；断言为「冻结 record 的离线回放
  一致性」；**不得**把 gas/tip/blob 具体数值当契约。
- [ ] **T2.3 断言类型**（契约 §5.1）：`eth_gasPrice`/`eth_maxPriorityFeePerGas`/
  `eth_blobBaseFee` → lower median / `Selected`；`eth_syncing` → local / `Selected`。
- [ ] **T2.4 接入互锁与回放入口**：同 T1.5（移除这 4 个方法、接入新 feature）。
- [ ] **T2.5 验收**：同 T1.6。
- [ ] **T2.5b 提交**：独立 commit；message 写清 `eth_syncing` 形态覆盖情况与未覆盖项。

---

## 阶段 3：组 3 — block-reference（safe/finalized 各自锚定）

**方法**：`eth_getLogs`、`eth_feeHistory`
**feature**：`read-blockref`（名称待定）

- [ ] **T3.1 契约复核**（§2）：**必须** `safe`/`finalized` 各自锚定，**不得**落 `latest`。
- [ ] **T3.2 tag 可用性核查 + 共同 reference**（§2.1、§2.1bis）：按 T0.3 的
  方式取得共同 reference 数值高度；不支持则换源或登记未覆盖。
- [ ] **T3.3 anchor 语义已定**（§2.2，直接执行）：
  - `eth_feeHistory`：参数位置 1 的移动标签改写为 **reference 高度**（不是 hash）；
  - `eth_getLogs`：`fromBlock`/`toBlock` 为 **高度改写**；
  - expect 断言的是「高度改写」，**不得**断言为 hash 改写。
- [ ] **T3.4 plan + 捕获**：
  - `eth_getLogs`：至少覆盖 `safe` 与 `finalized` 两个基准；**明确空数组与
    非空日志对象两种目标**（空数组不覆盖日志对象形状）；**用窄范围限制体积**；
    保留真实的限流或失败记录。
  - `eth_feeHistory`：覆盖 `safe`/`finalized` 两个基准。
    **注意**：官方允许返回**少于请求区间**，而当前 admission 要求数量完全
    相等 → **保留这种拒绝结果，不为捕获改规则**；ratio/reward 的浮点差异
    也可能导致冲突，如实记录。
- [ ] **T3.5 expect**：`transport: "http"`；区分**原始请求**与 **prepared
  （锚定后）请求**，断言 anchor 处理结果。
- [ ] **T3.6 接入互锁与回放入口**：同 T1.5（移除这 2 个方法 → 此时 `noCapture`
  应为空；接入新 feature 与回放入口）。
- [ ] **T3.7 验收**：同 T1.6，并额外确认 `TestCapturedExpectTransportRejectsWSS` 仍通过。
- [ ] **T3.8 提交**：独立 commit。

---

## 阶段 4：收尾

- [ ] **T4.1** 确认 `noCapture` map 为空，且 `TestRegisteredMethodSampleStatus`
  对全部 `RegisteredNames()` 断言「有归档」。
- [ ] **T4.2** 按契约 §6 的两级完成定义核对：
  - **L1**（方法已有真实归档）：`noCapture` 清空 + 互锁通过；
  - **L2**（必需 case 全部覆盖）：`safe`/`finalized` 各自锚定、`eth_syncing`
    两态、logs 空/非空目标。
  - **缺 L2 时可提交部分成果（L1），但不得宣称全部完成**；未覆盖项如实登记。
- [ ] **T4.3** 更新 `docs/design/project-roadmap.md` 状态，列出实际 feature/batch-id
  （**进度只维护 roadmap，不回写本文件为计划**）。
- [ ] **T4.4** 全量验证：`gofmt -l .` 空、`go vet ./...`、`go build ./...`、
  `go test ./... -count=1` 全绿无跳过、`go mod tidy -diff` clean。
- [ ] **T4.5** 提交收尾文档。

---

## 全局纪律（必须遵守）

1. **禁止外部网络 I/O** 出现在测试运行中；真实录制只在捕获阶段一次，提交的是冻结 record。
2. **禁止盲目 `-update`**；每处 expect 变更必须能解释为对冻结 record 的回放。
3. **不得**缩分母、降门槛、改 typed gate、自动补字段。
4. **不得**新增 WSS golden 或 WSS 变体。
5. **51 处品牌字符串零改动**。
6. 范围外问题单独登记，不顺手修。
7. 每个阶段独立提交，阶段之间不混合。
8. 每阶段完成后必须贴：`gofmt -l .`、`go vet ./...`、`go build ./...`、
   `go test ./... -count=1`、`go mod tidy -diff`。

---

## 预期实际卡点（执行时预警）

1. **Chainlist 来源不能跨链混成一次投票**。replay 节点与 pool 固定 chain ID 1；捕获
   其他链需要独立批次与网络上下文。
2. **不同时间补录 `eth_syncing` 状态要用不同 case/批次**，不能把不同时间的观察
   拼成一次调用。
3. **换源应在新一次捕获前确定**；已经录到的失败**不能删掉后重算分母**。
4. **`eth_syncing` 对象态不能靠来源数量保证取得**。找不到时如实登记覆盖缺口，禁止合成 record。
5. **`eth_feeHistory` 短区间与浮点差异**。官方允许返回少于请求的区间，而当前 admission
   要求数量完全相等 → 保留拒绝结果，不为捕获改规则；ratio/reward 差异也可能冲突。
6. **`eth_getLogs` 空数组不覆盖日志对象形状**。明确空/非空目标，用窄范围限制体积，
   保留真实限流或失败记录。

---

## 执行报告需回答的问题（glm 必答）

1. 本批实际使用的 feature 名与 batch-id。
2. 每方法录制的源数量与该 case 的 `n`（Aggregate 入口投票成员数），确认 `n == len(sources)`。
3. 各方法的成功状态是否符合契约 §5.1（`Corroborated` vs `Selected`/local）。
4. **组 3 的共同 reference 如何取得、数值高度如何写入 plan、原 tag 上下文如何离线恢复。**
5. `eth_syncing` 是否覆盖两态；未覆盖则列出。
6. `safe`/`finalized` 是否在所有录制源可用；未使用/未覆盖的 case 及原因。
7. `eth_feeHistory` 是否遇到「返回区间少于请求」的拒绝；如何记录。
8. 是否存在无法完成的方法或 case，原因与登记位置。
9. 全量验证命令与结果（含 `go test ./... -count=1` 是否无跳过）。

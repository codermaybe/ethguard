# EthGuard 后续推进规划

本文件是唯一持久化计划，不另建执行计划文件。规则正文只在各自的设计文档
（`return-value-design.md`、`method-verification-pipeline.md`、`decisions.md`）；
本文件只记录方向、顺序、依赖和出口。moving-tag matrix 的可执行来源仍是测试，
文档只作对照。

本路线图的总约束：对外提供的值必须是方法契约定义的**聚合结论**。
它是多源观察经过请求绑定、准入、规范化、受保护域编码和裁决后的产物，
天然可能不完美，但不能用某个 provider 的“相对可靠值”替代。代表源只能
作为审计锚点；它不能把来源原值伪装成聚合结论。若观察无法形成满足契约的
结论，返回 `Unknown` / 冲突错误并保留证据，不能降低门槛、缩小分母或改走
单源值来制造成功。

`Selected` 和 `Forwarded` 是显式的较弱状态，不属于上述聚合结论。它们只在
已有方法契约明确允许时保留，并且必须让调用方看见状态差异；新方法的默认
出口不以它们替代 `Corroborated`。这条约束优先于“尽量返回一个值”的兼容性
目标。

## 当前基线

- EthGuard 是多源 Ethereum Execution RPC 客户端，不是轻客户端。
- `Corroborated` 表示 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 cryptographic finality。
- `Unknown=0`；错误返回不得携带成功置信状态。
- 返回值框架已完成：代表源同源（RV0）、数量与 DATA 规范编码（RV1，0041）、
  五个对象方法规范编码（RV2，0042–0046）、键值合一（0047）、证据通道收缩
  （0048）。收据 `from` 口径以 0051 为准，revert 上下文分离以 0052 为准。
- T07 标准驱动闭环已完成：Core/Extension/Diagnostics 模型（0050）、门槛内部
  动态计算（根客户端 quorum/mode 配置已移除）、单一评估 seam、Chainlist 形态
  探查、官方标准升级与 conformance 闭环、端到端默认化验收。
- T08.1–T08.7 已完成实现（`Decision.Revert` 承载 code/message，`Decision.Value`
  只保留 accepted data 投影，区块/交易 canonical encoder、Provenance API break
  和全注册方法 canonical 结果均已落地）；当前进入统一审计。
- 已知缺口：WSS 仍不能区分 error data 缺失与 `null`。含 `httptest` 本地监听在内的
  全量测试已在本机运行通过（`go test ./... -count=1`），replay 端到端批次无跳过。

## 已完成里程碑

下表只留结论与指针，验收证据（测试名、diff、测量数字）以对应提交的 git 历史为准，
不再在本文件展开。

| 里程碑 | 结论 | 指针 |
| --- | --- | --- |
| T02 | EIP-1898 第一切片、tag-aware negative block handling、pending local observation | `moving-tag-matrix.md` A 节 |
| T03 | `eth_getLogs` pending 范围与 `BlockByNumber(-1)` 保持本地拒绝；estimateGas/accessList 对象形态对齐 | 决策 0035/0037 |
| T04 | revert data 五态比较键；三批离线样本归档；`TestMethodContractsForbiddenClaims` 互锁 | `testdata/*/2026-09-23` |
| T05 | Reference 新鲜度/失败契约；`DecisionEvent` 双模型；不新增 evidence helper（0038） | 决策 0026/0038 |
| T06.0–T06.1 | 0039 规则 4e 冻结为方案 (i)；matrix `Tests` 列 `go/parser` 互锁；不引入 DSL（0040） | pipeline 第 1.1 节，决策 0039/0040 |
| Anchor Transport Seam | HTTP/WSS 共用 anchor 与 dial，语义不分叉 | `anchor-transport-seam.md` |
| T06.2–T06.5 | replay 基础设施、三批 golden、4e 落地、读方法批次扩展 | `replay/`、三批 `expect.json` |
| RV0 | 代表源改为捕获顺序第一人，Value/Evidence/Withheld 同源，锚点 hash 小写 | `8aac099` |
| RV1 | 数量与成功 DATA 规范编码（0041） | `48e2c64` |
| RV2a–RV2d | feeHistory、logs、accessList、receipt、blockReceipts 规范编码；落选簇诊断与编码摘要 | `dee2208`、`f8b4280`、`cade5a3`、`2583c28`、决策 0042–0046 |
| 键值合一 | 五个对象方法比较键即规范字节（0047）；证据通道无代表源 Raw（0048） | `574bfd9`、`5a0f53b` |
| 写路径 | `eth_sendRawTransaction` 相对信任广播契约，不做生命周期管理 | 决策 0049 |
| T07.0–T07.2 | Core/Extension/Diagnostics 模型；门槛内部化，用户 quorum 配置移除；单一评估 seam | 决策 0050 |
| T07.3–T07.5 | Chainlist 形态探查；`cmd/specbundle` + specdiff CI 门禁 + projector/encoder coverage gate；端到端默认化验收 | 决策 0051（含 receipt `from` 回归官方字段） |
| T08.1 | revert context/value 分离：`Decision.Revert` + context-free Value + replay 禁止 envelope 回流 | `9c00b87`、决策 0052 |

## T05：运行期证据与可观测性

T05 已完成。reference 的新鲜度与失败语义由决策 0026 固定：每次调用内建立、无复用、失败停在 Prepare；对应测试为
`TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout`、
`TestReferenceEstablishedFreshPerCall`、
`TestReferenceFailureEvidenceIsUnknownWithObservedAt`。

`DecisionEvent` 与 `rpc.Status` 保持双模型；失败事件使用 `failed`，不把它加入置信状态枚举。对应测试为
`TestDecisionEventStatusMatchesSuccessClass`、
`TestDecisionEventFailedStatusForClassifiedErrors`、
`TestStatusStringSetExcludesFailed`。Prometheus label 迁移由
`TestDocumentedDecisionStatusLabelsMatchEmissions` 固定；决策 0038 记录了不新增通用 evidence helper 的选择。

## T08：revert Envelope 与多维比较键（实现完成，待统一审计）

依赖：T07 全部完成。状态：T08.1–T08.7 实现已完成，待统一审计；
不回退 T07 的标准生成、schema 门禁、projector/encoder coverage gate 或
T08.1 的 `Decision.Revert` 分离。

总出口：把仍依赖代表源原文的 revert/错误结果和未迁移对象，按方法契约拆成
可审计的多维比较键；不把错误文本、厂商扩展或来源原文误当成官方 Core。
每一步都以“聚合结论或明确 Unknown/Conflict”为出口，不允许把单源
Selected/Forwarded 伪装成已聚合值。

一次只开工一个已解除的任务，每个任务单独提交（变更摘要、`go test` 命令、
失败路径、样本/golden 变化、未解决风险）。

### T08.2 固定多维键与 typed adapter 边界（已完成）

- 把多维键限制在方法内部的固定维度，沿用固定调用分母和动态门槛；
  不得新增用户 quorum/mode 配置，不得跨来源拼接业务对象。
- 明确 typed adapter 从裁决产物投影的边界：成功 DATA 与 corroborated revert
  的 data 投影走 Value，code/message 只经 `Decision.Revert` 恢复 `RevertError`。
- 出口：`eth_call` 成功/revert/弃权/冲突/pending 全矩阵经真实 Aggregator +
  adapter 验证；replay 断言 provider envelope 不进入 Core Value。
- 本轮已补真实链路覆盖：`TestT082CallSuccessUsesCanonicalDataThroughAdapter`、
  `TestT082CallRevertKeepsContextOutOfCoreValue`、
  `TestT082CallRevertWithheldDataKeepsProviderEnvelopeOutOfValue`、
  `TestT082CallConflictStaysUnknownThroughAdapter`、
  `TestT082CallPendingRemainsSelected`；replay 的
  `replayValueAgrees` 同步拒绝 `code/message` 进入 Value。

### T08.3 RV3b 区块重编码（已完成）

- 方向已锁：区块 Value 只重编码准入已核对过承诺的内容；`size`、
  `totalDifficulty`、厂商字段不进 Value。
- 必须经真实区块 policy 证明 0047 Unresolved 项：窄键（`number|hash`）下两份
  同键异体 payload 的聚类与 `finish()` 守卫行为，以及 Value 仍是聚合产物。
- 出口：同头异体、空块、withdrawals、历史叔块的真实 policy 测试 + golden diff。
- 首个切片已接入 `block.Encode` 与 `WithCanonicalObject`：区块头和已出现的交易、叔块哈希、
  withdrawals 走 typed canonical encoding，`size`、`totalDifficulty`、`from` 和厂商字段被排除；
  policy 测试已锁定窄键同值异体聚类与 `finish()` 守卫失败路径；block replay golden 已加入
  canonical Value 的 SHA-256。replay 端到端批次已在本机全量通过，无跳过。

### T08.4 RV2b3b 交易编码（已完成）

- 前置决策二选一并记录：沿用比较键佐证区块上下文（明确非 inclusion proof），
  或坚持承诺验证并设计所需验证与 typed pending 输入。
- 在决策落地前不得以编码器暗中选择；`DecodeTransaction` 的 pending 判定口径
  随决策同步。
- 出口：决策记录 + mined/pending/异体交易的真实 policy 与 adapter 测试。
- 决策 0053 选择沿用现有交易比较键作为区块上下文佐证，明确不提供 inclusion proof；
  `transaction.Encode` 已接入 typed/schema Value，pending 仍由缺失或 null 的
  `blockNumber` 判定；真实 adapter/replay 已在本机全量通过。

### T08.5 Step4：删除 `projection.StandardFields`（已完成）

- 前置：T08.3、T08.4 完成（区块与交易已重编码，不再依赖第二份字段表）。
- 出口：`method-rules.md` 与 coverage gate 无第二字段源残留；纯包测试、全量编译、
  `go vet` 与全量运行验收均已通过。

### T08.6 Step6：逐源闸门合成（已完成）

- 把准入、EncodeCheck、normalize 等逐源闸门合成一段；顺序仍是先编码检查、
  再分簇、一次裁决，不因合成改变语义。
- 含 0048 的 API 破坏（`RevertError.Code`/`Message`/`ErrorCode()` 删除，
  `Decision.Evidence` 改名 `Provenance`），已由决策 0054 记录。
- 出口：adapter 兼容断言、编译/vet、全量测试与全量运行验收均已通过，
  破坏清单已记入决策 0054。

### T08.7 RV4：已注册方法不再有 `raw`（已完成）

- 断言 `method-rules.md` result 列无 `raw`（pending-local/median 的 Selected
  语义保留，不升为 Corroborated）。
- 出口：第 9 节断言生效；纯包测试、全量编译、`go vet` 与全量运行验收均已通过。

开放项（不挡住上面任何一步）：`median.go` 的 `equal` 是否改为按键筛选；
接入套件的形状变体能否从 `internal/spec/methods.json` 生成代替手工补语料；
是否存在经 `replace` / `go.work` 引用本库的外部使用者（决定 API 破坏要不要
弃用窗口）。

## T08 审计遗留（close-out，完成前不新接方法）

状态：1–3 已完成；4（WSS）已完成评估（当前架构不可行，缺口保留），5 未开始。T08 统一审计（全量测试通过）后登记。方向判定：已注册覆盖已
完整对应目标面（proxyd 重写表 + 本地/变量/身份组）；剩余未注册候选多需新信任
模型（filter/subscription 有跨调用状态、`eth_getProof` 需证明验证、debug/trace
属节点本地），或暂无需求。因此下一阶段先收尾证据完整性，不扩方法数。

1. anchor 内部拼写：`restoreAnchorSourceValue` 把 anchor 内部 decision 还原成
   代表源 wire 拼写。已补换序测试 `TestAnchorRequestHashStableWhenSourceOrderSwaps`，
   证明发出的锚点请求 hash 不随代表源拼写变化（请求路径小写处理已在 RV0 锁定）。
2. golden 字节可见性：已选择保留 `block-receipt-tx` 的 canonical Value SHA-256，
   由决策 0055 固定；运行时 `replayValueAgrees` 继续做精确字节断言。
3. 0054 半句限定：已补充 `Observation.Evidence` 保留，退役的只是 Decision
   层旧字段，见决策 0054。
4. WSS 缺失/`null` 坍缩（已完成评估，单独立项，不进方法任务）：HTTP 已在收集层保留
   原始 error 对象（缺失记 `Data == nil`，JSON `null` 记 `Data` 为 `null`）；WSS
   仍走 geth 解码路径，两者在进入观察前坍缩。目标二选一并记录：WSS 同样保留
   原始帧并通过四态回放，或书面确认不可行、缺口继续携带。已选择并记录后者，见
   决策 0056；WSS golden 继续拒绝，HTTP 四态回放保持有效。
5. 轻客户端对照（方法契约 0.7 / 决策 0024 收尾项）：对照适用版本规则并记录
   适用/冲突/不适用/未决，仍未执行。排在捕获批次之后。

以上 1–3 为小收尾，可与捕获批次并行；4–5 单独立项。执行顺序为：先完成
close-out 1–3，再完成 11 个未捕获读方法的正式捕获批次，随后做轻客户端对照，
最后再议是否开启 filter/subscription、`eth_getProof`、debug/trace 等新方法主线。

## 排后：未捕获读方法的捕获批次

状态：排在 T08 之后。2026-09-26 的形状探查（`internal/rpcsample/probe/`）只是
观察报告，不是 `testdata` 批次，没有 `expect.json`，不解除任何方法，
不得直接升成 golden，不得合成样本。

- 11 个读方法仍无正式捕获批次：`eth_getLogs`、`eth_feeHistory`、`eth_chainId`、
  `net_version`、`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、
  `eth_blockNumber`、`eth_syncing`、`net_peerCount`、`net_listening`
  （以 `TestRegisteredMethodSampleStatus` 钉住的名单为准）。
- 补批次时仍须满足六项进入条件，`safe`/`finalized` 各自锚定，不落到 `latest`。
- `balance-safe-canonical` 本批捕获达不到 quorum，补采达标批次前保持冲突结论，
  不降 floor。

## 暂不推进

- Ethereum light client、BLS、sync committee、trusted checkpoint。
- provider 权重、信誉和 failure-domain 模型。
- pool 大规模重构。
- 跨调用 anchor cache。
- 仅为扩大方法数量而新增 RPC 方法。

## 每个新方法的进入条件

新增 RegisteredMethod 必须同时提供（逐项核验见 `method-verification-pipeline.md` 第 5 节）：

1. 官方请求和响应契约；
2. moving-tag / block-reference 处理矩阵；
3. admission 与 normalization 边界；
4. 成功、冲突、无效、pending 和 provider error 测试；
5. 明确的 provider sample 状态；
6. 对外说明它提供的是 corroboration evidence，而非 Ethereum consensus proof；
7. 进入规范编码的方法在 `rpc/methods/admission_suite_test.go` 加一行，并在方法规则索引声明键来源。

## 通用执行纪律

1. 只修改任务范围内的代码、测试、样本和文档；范围外问题单独记录，不顺手修复。
2. 不引入 `MethodContract` DSL、代码生成、`suite.RunAll`、新的 quorum 模式或 provider 权重。
3. 不通过删减投票分母、降低门槛、改变 typed gate 或自动补字段来让测试变绿。
4. 所有 replay 离线运行；允许进程内 `httptest`，禁止外部网络 I/O。
5. 审计拒收：只测 Policy 层而无 Aggregator/typed-read 端到端证据；用 mock 替代
   生产路径的 error/Admission/anchor；新增第二事实源或扩展 Markdown 语法；
   改样本无 sha256/index/golden 说明；把 provider-backed evidence 写成
   canonical/finality/consensus；无可复现 `go test` 命令与失败路径验证。

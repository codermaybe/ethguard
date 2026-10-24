# EthGuard 后续推进规划

本文件是唯一持久化计划，不另建执行计划文件。进度与状态只在本文件维护；
派发给执行者的任务清单（如 `capture-tasks.md`）是**派发副本**，不是计划，
不承担进度记录。规则正文只在各自的设计文档
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
  和全注册方法 canonical 结果均已落地）；统一实现审计与全量测试已完成。
- WSS 不能区分 error data 缺失与 `null`：这是决策 0056/0056.1 记录的显式限制
  （评估已结案：在当前 seam 不可行，属显式限制而非待办缺口，不阻塞捕获批次）。
  含 `httptest` 本地监听在内的全量测试已在本机运行通过
  （`go test ./... -count=1`），replay 端到端批次无跳过。
- 读方法捕获：阶段 0（`reference_context` 契约、共享 replay height resolver、
  prepared-request 断言）已落地（`2cd069e`）；组 1 `read-local`、组 2
  `read-volatile`、组 3 `read-blockref` 均已完成真实捕获与真实源整合验收。
  `noCapture` 已清空，11 个读方法 L1 全部完成；L2 仍缺 `eth_syncing` 对象态，
  批次级 L2 保持未完成。

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

## T08：revert Envelope 与多维比较键（已完成）

依赖：T07 全部完成。状态：T08.1–T08.7 实现、统一审计与全量测试均已完成。
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
接入套件的形状变体能否从 `internal/spec/methods.json` 生成代替手工补语料。

已关闭：是否存在经 `replace` / `go.work` 引用本库的外部使用者（决定 API 破坏
要不要弃用窗口）。核实结论：不存在外部使用者。依据：`go.mod` module 为
`github.com/codermaybe/ethguard` 且无任何 `replace` 指令；本仓库及工作目录
不存在 `go.work`；本机 `$(go env GOPATH)/pkg/mod/cache/download/` 中无任何
`ethguard` 命中（本模块从未被发布到 module proxy，也从未被其他模块拉取）；
仓库无任何版本 tag。因此本库是唯一导入自身包的模块，API 破坏（0048/0054：
`Decision.Evidence` 改名 `Provenance`、`RevertError` 收缩）无需弃用窗口。
结论与有效期约束见决策 0057：一旦本模块未来被发布或出现外部使用者，该
前提失效，后续破坏须重新适用弃用窗口纪律。

## T08 审计遗留（close-out）

状态：1–3 已完成；4（WSS）已完成评估并由决策 0056 结案，5 以既有
`spec-baseline.md` 第 7 节初审结论结案。T08 统一实现审计与全量测试已完成；
WSS 保真和轻客户端复核均不阻塞新方法捕获。方向判定：已注册覆盖已
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
3.1 待改动字段清算（与 WSS 相关的讨论登记，不设前置闸门）：见
   `docs/design/wss-error-data-presence.md` 第 2 节。当前没有确定字段，
   因此不设出口闸门，也不阻塞 WSS 或任何其它工作；登记是信息性的，
   不构成排期承诺。
4. WSS 缺失/`null` 坍缩（已完成评估，单独立项，不进方法任务）：

   **先承认**（根因，逐帧核实 geth v1.17.5）：坍缩不发生在 `websocketCodec`，
   而发生在响应解码的类型化步骤。`jsonrpcMessage.Error` 是 `json.RawMessage`
   （`rpc/json.go`），原始 error 字节直到 `Client.CallContext` 取回 `op.resp`
   时都完整存在；是 `resp.decodeError()` 把 `error` 反序列化进
   `jsonError.Data interface{}`，才让“字段缺失”和“JSON null”同时变成 `nil`。

   **关键限定**：原始字节在 geth 内部存在，**不等于 EthGuard 能通过公开接口
   取得**。`rpc.ServerCodec` 方法/参数类型全部未导出（`rpc/types.go`、
   `rpc/json.go`），外部包无法实现；`DialOptions` 返回的仍是 geth `Client`，
   `decodeError()` 由它内部执行，替换 WebSocket dialer 绕不过这一步。

   **后改造**（分步，未开工）：方向是先在 pool 内建立自己的调用接口，再评估
   自行管理 WSS 请求响应或采用上游扩展点的成本，决定是否实现。
   **不预设实现行数，也不把“seam 建立”等同于 presence 已恢复。**
   完整分析见 `docs/design/wss-error-data-presence.md`。

   **当前业务影响**（不变）：在 `eth_call` 中，缺失和 `null` 都只对 revert 的
   `data` 维度弃权，不会进入 `Decision.Value`，也不会互相补全；execution/revert
   维度仍可正常聚合。混合 HTTP/WSS 时同一个线上 `data: null` 可能分别表现为
   `reverted|null` 和 `reverted|missing`，两者都不为 `data` 投票，核心结果语义
   不变；损失的是 wire presence、诊断和 replay 精度。不能把 `null` 当作空 bytes，
   也不能据此生成 WSS 四态 golden。
   - 近期最小切片（路线 1）：把 `ErrorData() == nil` 准确表述为“无法判断上游是
     省略了 `data` 还是显式 `null`”，不再声称“已确认缺失”；优先放在调用级
     诊断信息中，**不改裁决行为、不加新比较键**。若最终改 `RPCError` 字段，
     必须先定默认值与既有 `RPCError{}` 构造的兼容性。未知状态按是否取得保真
     信息判定，不能只看 URL 是 WSS。
   - 决策 0056 的结案边界（不引入第二套 WSS resolver、raw-frame 关联层或新的
     长连接信任模型，WSS golden 继续拒绝、HTTP 四态回放保持有效）在保真实现
     验收前继续有效；seam 建立本身不代表 missing/null 已恢复。
5. 轻客户端对照（方法契约 0.7 / 决策 0024 收尾项）：以
   `spec-baseline.md` 第 7 节已有初审结论结案——EthGuard 当前不是轻客户端，
   相关规则按不适用/未证实/待办分类，不形成轻客户端合规或最终性声明。只有未来
   改变产品角色时，才另立 T12 复核。

### HTTP capture hardening（已完成）

状态：**已完成**（提交 `a318263`）。`pool/capturingTransport` 的响应体捕获
已改为有界读取（`defaultCapturedBodyBytes = 32 MiB`，测试可用
`dialHTTPPreservingErrorDataWithLimit` 缩小）；超限产生可分类错误
`ErrBodyTooLarge`，截断 body 不进入 `preserveErrorData`；错误优先级为
① ctx 取消/deadline ② 超限 ③ 连接失败；`rawBodyBox` 单槽不变量显式化
（二次写入返回 `errRawBodySlotTaken`）。该改动只收紧资源边界，**未改变**
error-data 的 presence 语义、HTTP 证据范围或裁决门槛。下文保留原任务边界
说明作为历史记录。

<!-- 原任务边界（已实现）：
- 为响应体捕获设置明确上限；超限必须产生可分类的截断/捕获错误，不能把截断的
  body 当作完整 JSON-RPC error。
- 明确请求 context、读取上限和连接关闭之间的错误优先级；为无界调用路径补充
  防御性超时或明确要求调用方提供 deadline。
- 仅增加进程内 HTTP 测试：正常四态、超限、读取失败、取消和错误 body 恢复。
- 不顺手引入完整 raw-wire `Observation`，也不改变成功结果的 `json.RawMessage`
  路径。
-->

### WSS 原生 transport 后续立项（不进入 T08）

状态：后续候选，不进入当前执行顺序。决策 0056 已结案；本项目当前不因 WSS 的
`error.data` presence 缺口阻塞 T08，也不把完整 raw-wire 证据、订阅或写路径
重构提前并入本立项。

结论：**先承认、后改造。** 承认现状（决策 0056 的结案边界继续有效：不引入
第二套 WSS resolver、raw-frame 关联层或新的长连接信任模型），但把“不可行”
收窄为“授权根因已核实，修复机制与成本待评估”。缺口只影响 JSON-RPC error body
的字段 presence：`error.data` 缺失与显式 `null` 在 geth WSS 解码后都变成 `nil`
（精确根因见上文 4 与 `docs/design/wss-error-data-presence.md`）。成功结果仍以
`json.RawMessage` 进入聚合层；`eth_call` 是当前唯一把结构化 RPC error body
纳入方法级裁决的核心路径。候选设计边界（待单独立项确认，不视为已冻结的承诺）：WSS raw client
的接口位于 pool 的 transport seam，不向 Aggregator、Policy 或 typed
adapter 暴露传输类型。

前置说明：不再设“字段清算冻结前禁止改造”的硬前置；HTTP capture hardening
保持既定优先级，但不是研究 WSS seam 的技术前提。

后续评估可按三个阶段展开；具体实现顺序、代价和外部约束待单独立项确认：

1. **可先成立内部 transport seam。** 把 Source 对具体 `*ethclient.Client` 的依赖
   收窄为内部调用接口，接口返回 raw result、structured RPC error、`error.data`
   presence，以及可选的有界诊断捕获。当前生产代码中 Source 只依赖
   `Close`、`ChainID` 和一次 `Client().CallContext`；HTTP 的 presence-preserving
   adapter 与现有 geth WSS 路径先接入该 seam，保持业务语义不变。不得改变
   Quorum/动态门槛、Aggregator、Policy 或方法数量。
2. **只实现 WSS error-data 保真。** 新 adapter 只承担长连接、并发 request/response
   demux、缺失/`null`/具体 data 保留、context 取消、inflight 清理、read limit、
   close、乱序和断连错误传播。先不实现订阅，不改变写路径，不把 RawWire 放入
   `Observation`、比较键或 `Decision.Value`。进程内 WebSocket 测试必须覆盖四态、
   乱序、错误 ID、超时清理、连接关闭、malformed frame 和读上限。
   该阶段的 ROI 仅是诊断、证据和 replay 保真；`reverted|missing` 与
   `reverted|null` 都是 `data` 维度弃权，当前裁决结果不因此改变。
3. **最后决定 raw-wire 证据。** 若产品确实需要传输级审计，再让 HTTP/WSS 共享
   有界的可选 raw-wire capture；raw-wire digest 只属于诊断/replay，不属于规范
   Value 或投票键。完整响应字节包含 ID、空白和字段顺序，不能直接作为 canonical
   结果。通过同一套调用、取消、关闭和错误契约后，才升级决策 0056 并增加 WSS
   四态 golden。

额外审计项：HTTP 的 `capturingTransport` 当前通过 `io.ReadAll` 捕获完整响应体，
需要单独评估有界捕获、超限和截断语义。该项与 WSS presence 修复分开，不顺手改变
HTTP 的证据范围。

方案边界：不使用“快速扫描 ID”替代完整 JSON envelope 校验；不以 `sync.Pool` 的
零拷贝优化优先于证据切片的防御性拷贝；不为当前没有订阅接口的项目预先设计
Call/Subscribe 双连接池；不以 300–500 行实现量作为生产可行性的证明。写路径的
重连和未知结果语义必须单独验收，不能因 WSS 读取保真而改变交易广播契约。

决策 0056 已结案；以上 1–3 仅是后续评估框架，尚未实施。当前顺序：近期先把
“不知道”准确表达出来（不改裁决行为），后续再评估 transport seam；
HTTP capture hardening 保持既定优先级。WSS transport 在保真实现经进程内
WebSocket 四态测试验收前不升级决策，也不增加 WSS 四态 golden。最后再议
是否开启 filter/subscription、`eth_getProof`、debug/trace 等新方法主线。

## 排后：未捕获读方法的捕获批次

状态：**已收口（L1 完成；L2 缺 `eth_syncing` 对象态）**。阶段 0 已完成（`2cd069e`）：`batch.json` 的
`reference_context` 契约落地，replay 的 Policy 层与 Aggregator 层改用同一份
height resolver，prepared-request 断言覆盖“两层一致且等于 record 实际请求”。
组 3 的原阻塞项（nil resolver、logs/feeHistory 参数断言、共同 reference 载体）
全部解除。契约见 `docs/design/capture-golden-contract.md`（G1–G5：transport 只捕获
HTTP、anchor 按方法分类、断言类型显式声明、replay 离线、分母语义）。派发任务清单见
`docs/design/capture-tasks.md`（派发副本，不是计划）。契约已明确：WSS presence 缺口
**不阻塞**本批（默认 policy 未开启 `WithRPCErrorBodies()`），新捕获一律按
`transport: "http"`；但**这不证明 WSS 传输已验收**。

**组 1 状态：已完成（2026-10-20）**。`read-local` 已提交（`acf3578`，批次
`internal/rpcsample/testdata/read-local/2026-10-19`，5 方法 × 3 源，15 条 HTTP
记录），`noCapture` 已移除这 5 个方法，expect 回放入口已接入。真实源数据整合
验收（2026-10-20UTC，同一组端点 publicnode/drpc/1rpc，经 `ethguard.New` +
`Client.Call` 走注册策略；未用 typed `Client.BlockNumber` 的 anchor 派生路径
代替本批 `eth_blockNumber` 的 lower-median 语义）：

- `eth_chainId`：`Corroborated`，support=3；
- `net_version`：`Corroborated`，support=3；
- `eth_blockNumber`：`Selected`，support=2、reject=1，值 `26235191`；归档批次
  为 `26220751`，相差 14,440 块（约 2 天，符合两次捕获的时间差）。差异来自区块
  传播时序，policy 按 lower median 拒掉落后的源，未升为 `Corroborated`；
- `net_peerCount`：`Selected`（local，support=1、faults=1），符合 §5.1；
- `net_listening`：`Selected`（local，support=1），符合 §5.1。

复跑 `cmd/soak`（3 源、finalized anchoring、`-anchor-ttl 0`）得到同向结果：
`eth_chainId`/`net_version` 均 3/0/0 corroborated，`net_peerCount`/
`net_listening` 为 Selected；1rpc 在该轮出现 429 与瞬时 HTTP 410，未改变
local 语义。离线批次的 `expect` 回放与全量 `go test ./internal/rpcsample/... -count=1`
通过：真实响应与冻结记录不矛盾，组 1 至此结束。

**组 2 状态：已完成（2026-10-20）**。`read-volatile` 批次
`internal/rpcsample/testdata/read-volatile/2026-10-21` 由 5 个请求 × 3 源组成
（15 条 HTTP 记录），`noCapture` 已移除这 4 个方法，expect 回放入口已接入。
replay 实测与 expect：

- `eth_gasPrice`：`Selected`，support=2、reject=1（drpc 报价更高被拒）；
- `eth_maxPriorityFeePerGas`：`Selected`，support=2、reject=1（1rpc 非零值被拒）；
- `eth_blobBaseFee`：`Selected`，support=2、faults=1（drpc 免费档返回 HTTP 400，
  失败被保留，未补合成值）；
- `eth_syncing`（两个请求）：`Selected`（local，support=1），三源均为 `false`。

真实源整合复跑（同一组端点、`Client.Call`）得到同向状态：gas/tip 各拒 1 个
离群源，blobBaseFee 在 drpc 报错下仍由两个源选中，syncing 为 local `Selected`。
`eth_syncing` 的对象态在所有源均未出现，**本批未覆盖该形状**，按契约登记为
覆盖缺口，禁止合成 record。组 2 至此结束。

**组 3 状态：已完成（2026-10-20）**。`read-blockref` 批次
`internal/rpcsample/testdata/read-blockref/2026-10-21` 由 11 个请求 × 3 源组成
（33 条 HTTP 记录），`noCapture` 已移除 `eth_getLogs` 与 `eth_feeHistory`
（现为空）。reference_context 固定了两个 tag 的多数视图：safe 三源一致
（26235345）；finalized 为 s1/s2（26235313），s3 的 26235123 作为落后参考记录
保留。replay 实测与 expect：

- `logs-safe-*`：`Corroborated`，support=2；s3 对 range getLogs 返回
  "method not available"；
- `logs-finalized-*`：`Corroborated`，support=3；
- `logs-safe-hash`：`Corroborated`，support=3（blockHash pin 不经高度改写）；
- `fee-safe`：`Corroborated`，support=2（s3 HTTP 410）；
- `fee-finalized`：`Corroborated`，support=3；
- `fee-finalized-noPct`：`Corroborated`，support=2（s3 429 HTML）；
- `fee-short`：`Unknown`/`no_valid_response`；s2 的短区间被 admission 拒绝
  （`gasUsedRatio length 2 != requested 10`），s1/s3 归档错误。

真实源整合复跑（`WithFinalizedAnchoring` + `Client.Call`）得到同向状态：
logs 四类均 `Corroborated`（2 支持 + 1 fault），fee safe/finalized 3/0/0，
short 区间 `Unknown` 且保留 admission 拒绝。

本轮暴露并修复了三类跨阶段问题：

1. replay 的 safe/finalized reference 曾使用 legacy 全员一致严格锚，现改为
   `FinalizedHeadAnchor`（safe 多数、finalized 共同 reference），与生产一致；
2. 非对象 JSON error 与 HTML 429 body 现在按 transport/protocol fault 回放，
   不再 panic 或当作 JSON-RPC error；
3. `eth_feeHistory` 的 blockCount 只接受 hex/uint64 的旧口径会在 `int` 入参
   （真实 `Client.Call` 的常见写法）下 admission 失败；现与 methods 层一致，
   接受整数类型与 `json.Number`，并加回归测试。

组 3 至此结束。L1（每方法有真实归档并回放）对全部 11 个方法完成；
L2 仍缺 `eth_syncing` 对象态，批次级 L2 不能宣称完成。

范围外发现（未修复）：`cmd/soak` 的默认 `-anchor-ttl 1m` 与决策 0026
（禁止跨调用 anchor cache）冲突，直接运行会失败；组 1 验收显式使用
`-anchor-ttl 0`。该工具的默认值需后续单独修正，不并入捕获批次。

2026-09-26 的形状探查（`internal/rpcsample/probe/`）只是
观察报告，不是 `testdata` 批次，没有 `expect.json`，不解除任何方法，
不得直接升成 golden，不得合成样本。

- 11 个读方法均已有正式捕获批次，`noCapture` 为空，L1 完成。L2 唯一缺口是
  `eth_syncing` 对象态（组 2 未出现），不得用合成 record 补；批次级 L2 保持
  未完成。
- 先冻结按方法分类的 golden 规则：具有 block-reference 参数的方法（当前是
  `eth_getLogs`、`eth_feeHistory`）才要求 `safe`/`finalized` 各自锚定；无参数的
  chain/local/variable 方法不虚构 anchor。replay 回放冻结的 record，因此 gas、tip、
  blob fee、block number 等随时间变化的值可以做精确离线回放；golden 需要同时声明
  它断言的是 wire/shape、Selected/local 语义、Fault/Forwarded 状态还是跨源结果，
  不把跨批次数值相等当成契约。
- `balance-safe-canonical` 本批捕获达不到 quorum，补采达标批次前保持冲突结论，
  不降 floor。
- 捕获工作不是单纯补数据：仓库已有通用 `replayCapturedCase` 和
  `replayRegistry`，但仍需为每个方法补正式请求、policy/admission 装配、状态/形状
  断言、expect 文件和样本状态互锁；`noCapture` 清单和批次 ID 也必须随批次更新，
  不得盲目 `-update`。

## T09（立项，未开工）：场景矩阵与字段生命周期不变量

立项依据：组 1/2 的捕获表明，逐个真实样本只能证明 wire 形状，不能覆盖
“同方法同 key 不同 value”的组合空间；`return-value-design.md` 第 4.3 节
已明确，簇内逐字节相等是必要条件而非充分条件，字段进入依据、变异测试和
往返测试缺一不可。T09 把这两类验证补成独立里程碑，不与捕获批次混合。

目标：让“字段在准入 → 归一/比较键 → 分簇 → 编码/提交 → 投影 → typed
解码”的每一段处置都可枚举、可断言、可审计。测试只作为证据；闭包由结构
约束和 coverage interlock 保证，不试图用有限用例证明全部字段永远稳定。

### T09.1 决策场景矩阵（合成、进程内）

- 每个 `RegisteredMethod` 按 resolver 适用场景跑固定矩阵：all-agree、
  minority-dissent、outlier、single-fault、all-fault、conflict、notfound、
  pending/local。
- 用 `rpctest.Node`/现有 Policy seam 构造响应；断言 `Status`、`Support`、
  `Reject`、`Faults`、dimensions、representative 与 typed 结果。
- 无外部网络；不写入 `testdata`，不冒充 provider capture；不新增 DSL、
  代码生成或 `suite.RunAll`（0040）。
- 出口：矩阵覆盖全部 `RegisteredNames()`；新增方法缺矩阵行即失败。

### T09.2 字段生命周期不变量（重点）

- 每个方法的受保护字段在方法契约中列出进入依据（已有口径），并在每一阶段
  登记处置：required/optional、是否进比较键、是否进 Value、是否派生、
  是否域外（Extensions/Diagnostics）。
- 变形测试：键序、空白、hex 大小写、`0X`、JSON 转义、厂商附加字段重排后，
  比较键与规范 Value 不变；同一变体放到每个 pool 位置，已迁移方法的 Value
  逐字节不变，代表源按第 7 节变化。
- 变异测试：删除或篡改受保护字段必须使 Admit/EncodeCheck 失败；不得靠省略
  字段制造簇内字节相等，不得回退到其他来源的值。
- 往返：`typed(raw_i) == typed(Encode(raw_i))`；派生字段用
  `derive(protected(raw_i))` 校验编码结果。
- 守恒：每份 observation 的字段去向可追踪（Value / Extensions / Diagnostics /
  reductive deletion / admission-or-encode fault），不得无记录消失。
- 静态互锁：schema/encoder 新增字段而契约或测试未声明处置时 CI 失败；
  复用现有 coverage gate 与 admission index，不建第二份字段表。
- 出口：上述不变量覆盖全部已注册方法；WSS missing/null 仍按决策 0056 显式
  标注，不把传输缺口写成字段稳定。

### T09.3 证据边界

真实捕获仍是 wire 形状、限流、方言和字段缺失的唯一证据；T09 的合成场景
不进入 `testdata/*/<batch>`，不产生 provider 兼容结论。T09 不修 WSS
presence，也不改变裁决门槛、分母或方法数量。

建议顺序：T09.2 先覆盖已注册方法（不等待组 3）→ 组 3 捕获按同一字段 gate
审阅 → T09.1 全注册方法矩阵。两条轨道独立提交，均不与捕获批次混合。

## T10（优先立项，分析阶段）：外部可验证测试与 live 证据边界

状态：**优先立项，证据边界已确认，按下表分阶段执行**（2026-10-23 核查）。
T10.0 审计通过（2026-10-23，`f5b989b`）：提交范围、证据、示例和 run/step
reference scope 均已复核；T10.1 已解除暂停。
T10.1 实现完成（2026-10-23，已审计，`9848bd5`）：新增离线 `internal/livecanary`、
`cmd/livecanary manifest`、`manifest.schema.json` 和 schema/脱敏/可重建性
测试；T10.1 只消费本地 snapshot descriptor，远端抓取仍属 T10.2。T10.1 复审
修正了零值时间与非 Chainlist snapshot URL，并在 schema、snapshot、manifest 和
Build 路径一律拒绝。T10.2 已解除暂停。
T10.2 实现完成（2026-10-23，已审计，`cd888ed`）：新增 `livecanary fetch`，对固定
Chainlist URL 单次抓取（60s、32 MiB、无重试、拒绝 redirect），固定 selected
basis；registry 不可用或 selected 少于 3 时仍写 manifest，并以
`state=inconclusive`（exit 2）结束，不执行 plan。远端抓取与成员不足测试均
使用注入 transport，不触网。T10.3 已解除暂停。
T10.3 实现完成（2026-10-23，已审计，`1291773`）：新增不改变裁决语义的结构化诊断
seam（observed prepared request、source-attributed faults、collection faults）、
`ethguard.live-report/v1` 报告类型/schema/严格解析、fixed plan 执行器（finalized
anchoring、固定成员、30s step / 300s run、typed adapter、notfound 错误路径）和
`livecanary run`。报告由本次 manifest、响应和 evidence 直接生成；失败与
inconclusive 也写 `report.json`。执行器测试全部使用 `rpctest` 进程内节点或注入
client，不触网。T10.3 审计修正（2026-10-23）：报告中的 `manifest_sha256` 绑定
`ManifestBytes` 的实际 SHA-256，不再采用调用方传入的 claimed digest；执行器以
`ParseManifest` 的解析结果作为唯一执行 manifest，并校验传入 `Manifest` 与字节
结构一致，digest 或结构不一致时仍生成记录实际 digest 的 `schema_error` failed
报告，并新增两项回归测试。T10.4 已解除暂停。
T10.4 实现完成（2026-10-23，已审计，`9df9df1`）：canary 不变量在 report 路径强制执行。moving
tag 检查递归覆盖嵌套 params，prepared request 中残留任何 moving tag 都记为
`request_binding`；`corroborated` decision 与 step reference 的 support 必须达到
固定门槛 `Q = max(2, floor(N/2)+1)`，低于 Q 记为 `illegal_state` /
`reference_not_corroborated`；分母、代表源、错误/成功互斥、typed decode 和
shape drift 的既有断言均由新增 `invariants_test.go` 的进程内失败样例覆盖。
shape drift 只记录 `value_kind`、`shape_matches_plan=false` 和 `degraded`，不产生
state reason；可用性 fault 与 quorum 不足只产生 `inconclusive`。未修改生产裁决
语义，未用 mock 替代 live 采集路径。审计复跑 `go test ./... -count=1`、
`go vet ./...`、livecanary/aggregator/rpc 定向测试及 race 全部通过；未触网、未修改
历史 capture/expect/rpcsample。T10.5 继续批量执行，尚未审计。
T10.5 实现完成（2026-10-23，已审计，`ddbd78a`）：新增
`.github/workflows/live-canary.yml`，PR/push 仍只走 `ci.yml` 的离线门禁；live
canary 只在 schedule、release tag 和手动触发时运行，checkout 固定
`github.sha`，权限仅 `contents: read`，单 job 15 分钟超时，artifact 保留 90 天。
live job 先执行 `livecanary run`，无论状态都写
manifest/registry(可选)/report/run.log/SHA256SUMS 并上传；新增
`livecanary verify -dir` 离线核对 manifest/report digest、run_id、plan hash 和
registry artifact digest。非 passed 观察会在 artifact 上传后使 job 失败，
`inconclusive` 不会被显示为 passed。workflow 结构由解析 YAML 的离线测试
覆盖，不触网。审计直接在 `ddbd78a` 临时 worktree 复跑 `go test ./... -count=1`、
`go vet ./...` 和 livecanary/cmd race 测试全部通过；workflow 的 exact commit、
最小权限、超时/并发策略、90 天保留、失败后上传和非 passed 强制失败均已复核。
未触网、未修改历史 capture/expect/rpcsample。T10.6 继续批量执行，尚未审计。
T10.6 本地 live 诊断完成（2026-10-23，代码 `63d5ac7`，修复 `0fc22eb`）：首次
真实 Chainlist/RPC 运行发现 v1 会把 path 内嵌凭据选入并发布，已按契约升级为
`chainlist-sorted-v2`（path 过滤、artifact 脱敏、可重建性测试）。修复后的本地
run 完成全部 6 个步骤，identity/reference/两条 business/negative 均达到
`corroborated`，typed block 与 notfound 路径正确，所有适用不变量为 true；
22 条 `provider_unavailable`/`provider_rate_limited` 可用性原因使 state 为
`inconclusive`，没有 failed 原因。manifest/report/registry 由 `livecanary
verify` 校验通过。详细记录见
`docs/design/live-canary/runs/2026-10-23-t10.6-local.md`。
由于本机没有 GitHub API token，维护者选择仅本地取证，本次未生成远端 run 链接
或可公开下载的 artifact；T10.6 的 external live observation 验收仍未完成，不把
本地诊断写成 attested evidence。T10.7 外部复核同样保持待办。
T10.6 审计结论（2026-10-23）：**阻塞，未通过**。本地 bundle 的
`SHA256SUMS`、manifest/report/run_id/plan/registry digest 与 `verify` 结果均已独立
复核，状态保持 `inconclusive`，但它们位于作者机器的 `/tmp`，没有外部 runner 的
run 链接和可下载 artifact，不能满足 T10.6 的硬性出口。补齐外部 workflow run、
公开 artifact 与第三方可下载复核前，不得解除 T10.6，也不得把后续阶段记为最终
通过。
T10.6 外部验收补齐（2026-10-24，GitHub run
`39983945116`）：workflow_dispatch 在 GitHub-hosted runner 上以 exact commit
`7d99c72967e4252b498d810ea12b74f496c53d58` 执行，checkout 断言、构建、live plan、
artifact coherence verify 和上传均通过。下载的
`live-canary-39983945116-1` 未读取完整 registry；`SHA256SUMS` 对 manifest、
registry、report、run.log 全部校验通过，离线 `livecanary verify` 也通过。manifest
记录 run_id `01M5MR7W787F2CPPY3N5G1BF64`、github-hosted provenance、Chainlist
raw/artifact digest 和 `chainlist-sorted-v2` 的 66/12/54 选择；report 状态为
`inconclusive`，16 条原因全部是 13 条 `provider_unavailable` 与 3 条
`provider_rate_limited`，没有 `failed` 原因，六个步骤均执行且适用不变量均为 true。
最后的 non-passed enforcement 按契约使 job 为 failure，但 artifact 已先上传并可下载。
T10.6 的 external live observation 出口已满足；这不是 passed，也不证明 provider
长期可靠性。T10.7 仍等待不同 workflow/repository 或独立 runner 的复核。
T10.7 本地复跑完成（2026-10-23，代码 `63d5ac7`）：在干净 clone 的 exact commit
上用 `livecanary rerun` 执行 T10.6 的同一 manifest，不重新抓取 Chainlist；
manifest/registry digest、run_id、state、各步骤 decision 值、step reference、
typed 结果和全部适用不变量与原始 run 一致。差异仅为部分成员的
support/fault 归属和 7 与 6 条限流原因，归类为 provider 波动；报告 digest 因
时间窗口和证据不同而不同。记录见
`docs/design/live-canary/runs/2026-10-23-t10.7-local.md`。干净 clone 仍运行在
同一主机，且无 GitHub API credential，因此外部 workflow/repository 的独立复核
仍未完成，T10.7 不标记为通过。T10.8 文档收口继续进行。
T10.7 预审结论（2026-10-23）：实现边界无阻塞。`rerun` 只解析既有 manifest，
固定原 selected basis，明确不重新抓取 Chainlist，并保留原 run_id/manifest digest；
本地复跑的差异已正确归类为 provider 波动。由于 T10.6 尚无外部 runner/artifact，
且 T10.7 自身也尚无独立 runner/repository，最终验收保持挂起，不得视为通过。
T10.7 CI 实现完成（2026-10-24，待维护者人工触发）：新增 `livecanary compare`，
生成 `ethguard.live-comparison/v1`，把 code/registry 差异与 provider 波动分开并
保留全部差异；新增 `.github/workflows/live-canary-recheck.yml`，通过
`workflow_dispatch` 接收 source run/artifact，校验源 artifact 的 SHA256SUMS
和 `verify`，checkout manifest 记录的 exact source_commit，使用该 commit 的
`rerun` 执行同一 manifest，再用当前 workflow commit 的 `compare` 生成差异报告，
上传 recheck bundle（90 天保留）。compare 遇到 code/registry 差异或不可重复时
使 job 失败；仅 provider 波动时保留差异但允许 job 通过。最终 T10.7 验收仍需
维护者手动 dispatch（例如 source_run_id `39983945116`、artifact
`live-canary-39983945116-1`）并下载复核 recheck artifact；在此之前不标记通过。
T10.7 最终审计通过（2026-10-24，GitHub run `40078416681`）：source artifact
下载、SHA256SUMS、原始 verify、manifest source commit checkout、同 manifest
rerun、recheck verify、compare 和 recheck artifact 上传全部成功；最终
`enforce repeatability` 被跳过，表示 `comparison.json` 的 `repeatable=true`，
没有 code/registry mismatch。rerun 日志中的 `inconclusive`/exit 2 属 provider
可用性结果，已由 workflow 的 continue-on-error 保留，不是代码失败。artifact
`live-canary-recheck-39983945116-40078416681-1` 保留为 T10.7 复核证据；该 run
使用不同 workflow 的 GitHub-hosted runner，满足独立 workflow 出口。T10.7 不宣称
provider 长期可靠性或独立性。
T10.8 完成（2026-10-23，待审计）：README 与 README.zh-CN 增加证据分层说明；
`moving-tag-matrix.md` 把 soak/capture 列改名为 observation evidence，并明确其
为历史本地观察/形状证据；`wire-forms.md` 同样标注重命名为历史本地样本，更新
legend 与 caveat。文档统一声明：离线 regression 是唯一 CI-backed 类别，
`Corroborated`/`finalized` 不是共识或 finality，单次 live run 不构成兼容性、
独立性或长期可靠性结论。未新增方法，未修改 WSS presence 或 reference policy。
已知非目标仍是：不证明 consensus、canonicality、finality、provider 独立性或未来
可用性；不把本地 capture/soak/单次 live run 升格为外部证明。
T10.8 预审结论（2026-10-23）：文档边界无阻塞。README、README.zh-CN、moving-tag
matrix、wire-forms 和 roadmap 均把离线回归、本地观察、external live observation
与 attested evidence 分层，明确 `inconclusive` 不是成功，也未把一次 live run
写成兼容性、共识、finality 或 provider 独立性证明。最终验收仍依赖 T10.6 外部
artifact；该 artifact 已于 2026-10-24 由 run `39983945116` 补齐，文档收口不再
依赖新的 live 请求。
T10.8 最终审计通过（2026-10-24）：T10.6 external artifact 与 T10.7 独立
workflow 复核均已记录；README、README.zh-CN、moving-tag matrix、wire-forms
和 roadmap 的证据措辞没有把 regression、本地观察、单次 live run 或
`Corroborated`/`finalized` 升格为兼容性、共识、finality 或 provider 独立性证明。
T10.8 证据状态收口（2026-10-24）：T10.7 外部复核通过后，README、README.zh-CN、
moving-tag matrix 与 wire-forms 中原先“尚无外部发布 artifact”的表述更新为已存在
GitHub-hosted artifact，并明确它们只是一次 `inconclusive` 观察和同 manifest 复核，
不升级历史 capture/soak 行，也不构成兼容性、共识、finality、provider 独立性或
长期可靠性证明。路线图证据表同步更新。

T10 当前证据状态：

| 层 | 状态 |
| --- | --- |
| Offline regression | `ci.yml` + `go test`/`rpctest`/replay；T10.4/T10.5 门禁已提交 |
| Live observation（本地诊断） | 2026-10-23 本地 run，state `inconclusive`（可用性 fault），全部不变量为 true；记录已提交 |
| External live observation | GitHub run `39983945116`，state `inconclusive`；artifact digest 与 report/manifest/registry 绑定已复核 |
| Attested evidence | T10.6 artifact（run `39983945116`）已下载并校验；T10.7 独立 workflow 复核（run `40078416681`，artifact `live-canary-recheck-39983945116-40078416681-1`）为 `repeatable=true`，差异仅为 provider 波动 |
T10 现在是路线图的优先问题；T09 的本地场景和字段不变量仍是实现正确性验证，
但不能单独关闭真实环境验证，也不能把本地回放结论写成外部可信证据。
本节记录已核实的问题、证据分层和第一阶段出口；reference policy 仍是独立的
临时参考方案，不因 T10 自动定案。

### 架构负债：规范来源与客户端依赖（决策 0060）

Execution APIs 是方法、参数、响应字段和官方标准边界的规范依据；它不定义
provider transport、WSS 细节、错误解码、typed client 行为或完整兼容性。当前
go-ethereum 是事实上的实现和类型参考，不是上游真值的规范来源。这个差距必须
作为架构负债保留：一方面是 provider 行为缺少同等完整的规范，另一方面是 pool
和 transport seam 仍暴露具体 `ethclient.Client`，而 typed value/codec 依赖
go-ethereum 类型。

需求已记录但方案未冻结：保持公开 ethclient-shaped 兼容，先建立小型内部 RPC
连接 seam，再逐步评估 transport、wire-error 和类型替换；不得在 T10 证据期间
进行大爆炸重写，也不得把自有实现冒充更强的上游标准。完整迁移必须单独量化
transport、WSS、Ethereum 类型、密码学和测试 fixture 的成本，并逐片通过现有
offline、adapter 和 live evidence 门禁。
范围澄清：API 与 Ethereum 语义类型继续跟随 go-ethereum；不自建平行的
Block/Receipt/Transaction、hex、共识编码或密码学类型系统。迁移只优先掌控
连接生命周期、HTTP/WSS、deadline、raw error-data presence 和来源证据归属，
并通过内部 seam 保持现有 go-ethereum-facing 兼容表面。

### 背景

外部反馈提出：`latest`/`safe`/`finalized` 是 provider 报告，不是 finality；
anchor 协商与业务断言应分层；测试应确定、可控；并建议 pinned
ReferenceContext、MockChainState、五类异常和 TestBatch 三元组。

需要保留的方向：anchor 与业务测试分层；anchor 作为一等测试对象；少量
端到端 canary。不能直接采纳的点：把 pinned ReferenceContext 注入 resolver；
用 Chainlist 实时成员替代固定分母；把 silent stagnation / provider tag
downgrade 写成可检测；用 live 测试替代 replay；新增 ConfirmationSnapshot
作为第二事实源。

真正待评估的问题：本地捕获 + replay 的信任模型是否足以支撑“实践可靠”的
测试流程？live canary、Chainlist 发现和 capture provenance 应放在哪一层？
T10 是独立里程碑，还是 T09.1 的扩展？

### 本轮核验结论（2026-10-23）

外部反馈的核心问题是真实的，但其中几项需要按“公开远端”和“本地工作树”
分开判断：

1. 当前公开 `origin/master` 仍为 `8ea250b`，没有 `.github/workflows`；本地
   工作树已有 CI 文件，但它位于尚未推送的提交中。因此“公开远端没有 CI”目前
   成立，不能用本地未发布文件反驳。
2. `cmd/soak` 确实会实时读取 Chainlist 并请求公共 RPC，但当前只输出 stdout，
   没有把 commit、Chainlist 原文/hash、完整候选和选中成员、工具版本、运行结果
   绑定成不可变 artifact。它是 live 开发探针，不是可独立复核的质量证据。
3. 现有 `index.json` 与 record SHA-256 只证明提交文件彼此一致；它们没有证明
   响应由真实 provider 在指定时间产生，也没有防止事后由人工或 AI 生成/改写
   record 与 expect。因此现有样本只能称为**未认证历史观察**，不能称为第三方
   真实验证。
4. `go test`、`go vet`、replay 和 `rpctest` 能证明实现路径在固定输入下的确定性
   与内部不变量；它们不能证明 provider 方言、限流、漂移、长期可用性或多源
   对抗行为。这个边界即使 CI 补齐也不会消失。
5. “AI 读取响应后自己填文件”是否发生，单靠仓库内容无法追溯；但当前产物缺少
   外部生成证明，因而验证流程必须按**无法证明来源**处理，而不是按真实捕获处理。

结论：反馈不是要求删除本地 replay，而是要求停止把不同证据类型混写。replay
保留为 regression oracle；live canary 负责 reality observation；只有在外部
runner 生成并发布带 provenance 的 artifact 后，才可称为外部可复核的 live 证据。

### T10 已确认的证据规范

T10 后续实现必须同时维护以下三层，不得用上一层冒充下一层：

| 层 | 证明什么 | 最低产物 | 不能证明什么 |
| --- | --- | --- | --- |
| Offline regression | 固定源码、依赖和输入下，生产路径可重复 | 外部 CI 的 commit 绑定日志、测试结果、覆盖/构建 artifact | 真实 provider 行为、长期稳定性 |
| Live observation | 指定时间、指定 Chainlist 快照和指定 RPC 集合上的实际行为 | 外部 runner 生成的 registry 原文/hash、候选/选中成员、plan、commit、工具和运行报告 | 共识、canonicality、finality、provider 独立性 |
| Attested evidence | 上述 live 产物没有被本地手工替换，且可由第三方复核 | 不可变 artifact、文件 digest、CI run/身份和保留链接 | 对抗共谋、统一错误、未来时刻的可靠性 |

具体规则：

1. PR/push CI 只跑离线测试、静态检查、构建和 replay；不得把实时 RPC 放进普通
   `go test`，避免把不确定的 provider 状态伪装成确定性回归。
2. 定时或 release live job 必须在远端 runner 上运行，实时获取 Chainlist，保存
   原始 registry、registry SHA-256、过滤理由、完整候选列表、确定性选中列表、
   请求计划、源码 commit、Go/tool 版本、运行时间、每步结果和失败分类。
   GitHub-hosted runner 能证明执行环境脱离作者本机，但本身不等于独立审计；若
   需要更强的第三方属性，应允许独立 workflow/repository 按同一 manifest 重跑。
3. Chainlist 只负责发现，不负责信任和投票。一次 live run 在启动时固定成员快照
   和分母；列表变化产生新 observation，不能与另一轮直接合并或比较。
4. live job 不得让 AI 或人工填写 expect。报告必须由可审计的程序从本次响应直接
   生成；人工只能审阅 artifact，不能修改后再上传为通过结果。
5. 代码错误、请求绑定失败、静默回退、缩小分母、非法状态和证据缺失必须使
   live run 失败。公共 endpoint 限流、不可达或有效成员不足只能标为
   `inconclusive`，不能标成通过；release 门禁不能把 `inconclusive` 当成功。
6. live canary 断言业务请求等于对应的 run/step reference、状态属于方法允许集合、成员和
   门槛保持固定、失败保留在证据中，并记录 shape drift；它不承诺检测 uniform
   stagnation、colluding majority 或 provider tag downgrade。

### T10 第一阶段出口（分析完成后才实现）

第一阶段不修改 reference policy、方法数量或 `expect/v1`，先完成以下设计和
最小实现边界：

1. 定义 machine-generated live manifest/report 的 schema，并让 `cmd/soak` 或其
   独立包装器输出完整 provenance；stdout 不能作为唯一证据。
2. 增加远端定时 workflow：离线 CI 作为代码门禁，live job 作为单独 artifact
   产出；两者状态和含义分开显示。
3. 让 live job 固定最小成员数、超时、请求计划和失败分类；Chainlist 变化、
   provider 限流和不足 quorum 不得静默转为成功。
4. 为 artifact 提供 digest、run 链接和保留策略，确保第三方可以下载并按 commit
   重跑离线部分；不把 live 响应直接写回仓库的 golden。
5. 用一次真实远端 run 验证流程本身，再决定是否把 T09 的本地矩阵完成标记为
   “实现验证通过”。在此之前，T09 只能标记为本地 regression evidence。

### T10 行动规划表（交给执行 agent）

下表是 T10 的唯一执行顺序。经维护者授权后，T10.4–T10.8 可以连续执行，最后
统一进行逐项审计；每个 ID 仍必须独立提交并保留自己的 artifact、测试命令和失败
分类。任何一项都不得通过修改历史 capture、手填 expect 或降低门槛来“变绿”。
发现硬阻塞（安全、数据完整性、成员分母或 artifact provenance 破坏）时必须停下，
不得用后续 ID 掩盖前项缺陷。

| ID | 目标与 agent 必须完成的工作 | 必须提交的产物 | 硬性验收 | 依赖与禁止范围 |
| --- | --- | --- | --- | --- |
| T10.0 | 冻结 live canary 契约：定义 run 状态（`passed` / `failed` / `inconclusive`）、最小成员数、超时、请求计划、失败分类，以及“观察”与“证明”的文字边界 | 规范补丁；一份 machine-readable manifest/report 示例 | 每个字段有生产用途；`inconclusive` 不可成为成功；不含 finality/consensus 声明 | 先于代码；不改 reference policy、`expect/v1`、`batch.json` |
| T10.1 | 建立 provenance 生成器：绑定源码 commit、workflow/run、Go/tool 版本、Chainlist URL、抓取时间、原始 registry SHA-256、候选/过滤/选中成员、selection 算法版本、plan hash | `manifest.json` schema、生成代码、schema 测试、脱敏测试 | 同一输入可重建 selection；artifact 缺关键 provenance 即失败；不得由人工或 AI 填字段 | 依赖 T10.0；不把 sha256 当来源签名，不写入历史 golden |
| T10.2 | 实现 Chainlist 采集与成员固定：远端抓取原文，解析指定 chain，过滤非 HTTPS/明显凭据/不支持项，去重并按固定规则排序，固定本次 voting basis | registry 原文或安全脱敏副本、registry hash、候选/过滤/选中清单、过滤原因 | 运行开始后成员集合不可变；有效成员不足返回 `inconclusive`；列表变化不跨 run 合并 | 依赖 T10.1；Chainlist 只作 discovery，不作信任排序；不引入 provider 权重 |
| T10.3 | 将 live canary 从 stdout 升级为机器报告：执行 identity、anchor、pinned business、height/hash reference、typed adapter 和错误路径；记录每次 prepared request 与 reference | `report.json`、报告 schema、报告生成代码、离线报告解析测试 | 报告由本次响应直接生成；能核对 request/reference、状态、N、support/reject/fault、代表源和错误状态 | 依赖 T10.1/T10.2；不把实时值写成固定 expect，不在普通 `go test` 触网 |
| T10.4 | 增加 canary 不变量检查：业务请求必须绑定对应的 run/step reference；不得静默回落 `latest`、缩小分母、改门槛或把错误带成功状态；记录 shape drift | 生产路径断言、失败样例（进程内构造即可）、live report 中的 invariant 结果 | 代码错误/请求绑定错误/非法状态/证据缺失使 run `failed`；provider 限流、不可达、不足 quorum 只能 `inconclusive` | 依赖 T10.3；不修改生产裁决语义，不用 mock 替代 live 采集路径 |
| T10.5 | 编写外部 workflow：PR/push 运行离线 CI；schedule/release 运行 live canary；上传不可变 artifact，分离代码门禁和 live 观察状态 | workflow、artifact 清单、权限最小化、超时/取消策略、保留策略 | workflow 使用 exact commit；live job 保存 manifest/registry/report/digest；失败分类可在 UI 和 artifact 中复核 | 依赖 T10.1–T10.4；不让 live provider 波动阻塞普通 PR；不把 `inconclusive` 标为通过 |
| T10.6 | 首次真实远端验收：用 Chainlist 实时列表运行固定 plan，检查 artifact 完整性、redaction、请求绑定和状态分类 | 一次完整 run 链接、artifact digest、审计记录、发现的问题及修复提交 | 第三方可下载 artifact，按 commit 重跑离线部分；没有手工改写；最低成员数和失败边界生效 | 依赖 T10.5；不把一次成功 run 推广成长期可靠性或 provider 独立性证明 |
| T10.7 | 独立复核：在不同 workflow/repository 或独立 runner 按同一 manifest 重跑；比较 schema、selection、报告和失败分类 | 独立复核 run、差异报告、重复性结论 | 复核方无需作者本机状态即可重跑；差异必须分类为代码、registry 漂移、provider 波动或环境问题 | 依赖 T10.6；不引入第二套业务裁决或第二事实源 |
| T10.8 | 对外声明收口：更新 README、moving-tag matrix、wire-form 和 roadmap 的证据措辞；标明哪些只是 regression、哪些是 live observation | 文档 diff、证据等级说明、已知非目标 | 不再把 soak、capture 或一次 live run 写成普遍兼容、canonical、finality 或 consensus 证据 | 依赖 T10.6；不新增方法，不顺手修 WSS/reference policy |

#### Live canary 的固定执行行为

每次 live run 必须按同一顺序执行：

1. 记录 workflow、commit、工具链和 runner 信息；抓取并 hash Chainlist 原文。
2. 解析目标 chain，保存完整候选与过滤原因；按固定算法去重、排序、选取成员，
   固定 voting basis 和动态门槛，之后不得换成员或缩小分母。
3. 先跑 identity 与 reference establishment，再跑带 moving-tag 的业务请求；
   记录逻辑请求、prepared request、reference evidence 和实际响应之间的绑定。
4. 跑固定的 pinned business 集合：至少包括 block/state、logs 或 feeHistory、
   一个 typed adapter 路径和一个错误/不可用路径；具体方法由 T10.0 plan 固定。
5. 对每个结果检查状态允许集合、完整 voting basis 与各维度
   `support`/`reject`/`abstain`/`faults` 的证据守恒、代表源顺序、错误与成功状态
   互斥，以及是否发生静默回退或字段漂移；不得把跨维度计数简单相加冒充一个
   统一分母。
6. 生成报告和 digest；provider 层的不可达、429、超时和 quorum 不足进入
   `inconclusive`，代码不变量失败进入 `failed`，两者都不能写成 `passed`。

Live canary 不要求每次出现冲突，也不把没有冲突当作安全证明；它检验的是生产
路径在真实网络、真实 provider 方言和真实故障下是否保持既定边界。

#### Agent 交付纪律

- 一次只执行一个 ID；每个 ID 一个独立提交。
- 提交说明必须列出变更文件、运行命令、失败路径、artifact 位置、是否触碰真实
  网络，以及未解决风险。
- 不修改既有 record/expect 来制造通过；不把 live 输出复制成新的 golden。
- 不在测试中依赖开发者本机环境、未声明 secret、动态工作树状态或人工输入。
- 批量执行结束后由维护者按 T10.4、T10.5、T10.6、T10.7、T10.8 的顺序逐项
  审计；后项通过不替代前项证据。每个 ID 的提交边界、artifact 和失败路径必须
  可单独复核。

### 已核查事实

**归档状态**

- 截至本节写入前，仓库不存在 T10 文档、分支、决策或历史提交；工作树干净，
  分支只有 `master`、`backup-t06-stack`、`origin/master`，无 stash/tag。
- T09 是当前唯一已立项但未开工的测试里程碑（见上文 T09.1 / T09.2 /
  T09.3）。
- Reference Policy 仍是临时参考方案，不是决策、不授权实现
  （`reference-policy-proposal.md`）。
- 此前对话中的 T10-A/T10-B 是提案，不是已归档范围。

**Replay 实际行为**

- 离线测试 harness，不联网；用进程内 `rpctest.Node` 装冻结响应，再走生产
  policy / aggregator / anchor 代码；`expect.json` 是断言目标，不是执行输入。
- 加载时校验 index sha256、batch.json 严格字段、`reference_context` 成员的
  sha256/tag，以及 expect header（transport 只能 `http`）。
- 历史批次没有 `reference_context` 时，用 `scriptTaggedHeads` 固定模拟
  latest/safe/finalized 头（`0x10`/`0x0e`/`0x0c`）；这是确定性模拟 anchor，
  不是捕获 anchor。
- 有 `reference_context` 时，`reference_records` 的捕获响应驱动真实
  `StrictHeadAnchor`/`FinalizedHeadAnchor` 重算 reference；
  `expected_reference` 不注入 resolver。当前强断言的是 height；Hash/Status/
  Support 只做格式/成员校验，未与重算出的 `ReferenceEvidence` 逐项比较。
- 当前 `reference_context` 实际只覆盖高度改写的方法（`eth_getLogs`、
  `eth_feeHistory`）；hash 改写状态读取仍用固定 `taggedAnchor` fixture。
  `reference_records` 按 method 安装 handler，不是有序 transcript；同一来源
  同一 method 的多轮确认会覆盖，因此 fork/reorg 的多轮 confirmation 还不能
  被忠实回放。
- policy 层与 aggregator 层分开跑：hash 型方法 policy 层用固定
  `taggedAnchor`，aggregator 层用真实 anchor；height 型方法两层共用真实
  anchor height resolver；最后断言两层 prepared request 和决策一致。
- 不接收 WSS golden；不做 live capability probing；不证明 canonicality 或
  finality。

**Live 工具与 Chainlist**

- `cmd/soak` 和 `cmd/shapeprobe` 会直接请求 `https://chainlist.org/rpcs.json`；
  live 能力存在，但没有进入 `go test` 门禁，也没有与 replay 批次形成闭环。
- Chainlist 是社区列表，不是信任锚；动态成员会改变分母，破坏固定成员
  quorum 的可比性（G5）。固定成员测试必须使用 pinned snapshot 或 curated
  fleet。
- `cmd/soak` 的默认 `-anchor-ttl 1m` 与决策 0026 冲突，使用前需要修正。

**存储与 provenance**

- 当前 `internal/rpcsample/testdata` 约 1.4 MB、193 个 JSON，records 约
  832 KB；`docs/samples` 约 2.6 MB。存储不是主要矛盾，维护和审阅才是。
- `sha256` 只证明文件与 index 一致，不证明文件来自真实 provider；agent
  手工清理/写入的 record 仍然可能是伪造或过期的。当前没有 capture
  manifest、签名或 CI attestation。

### 候选边界（待评估，不是已同意方案）

- **D1 oracle 分层**：replay = regression oracle；live canary = reality
  oracle；Chainlist = discovery input；本地文件 = 带 provenance 的历史观察，
  不是真理。三层都不能单独构成“可靠流程”。
- **D2 测试分层**：pinned business（显式 hash/height，绕过 anchor）与
  anchor negotiation（真实 anchor + `rpctest` 舰队）分开；不注入 resolver
  输出，不新增 test-only 生产 API；少量进程内 canary 串联完整链路。
- **D3 anchor 场景矩阵**：all-agree、少数派高度分歧、同高 hash split、
  lagging（maxLag 内外）、tag `null`/`-32601`、all-fault、support 不足、
  confirmation mismatch、n=1/2/3/4 边界；按 `StrictHeadAnchor` 与
  `FinalizedHeadAnchor` 分别写期望，不把两者统一成一个语义。
- **D4 live canary**：使用 Chainlist 快照或 curated fleet，断言不变量
  （业务请求等于本次 reference、状态在允许集合、不静默回退/缩分母、证据
  保留），记录原始响应和 shape 漂移；作为发布/定时门禁，不替代离线
  regression。
- **D5 provenance**：capture manifest 记录 capture 命令/plan hash、endpoint
  身份 hash、Chainlist 快照 hash、时间、区块高度、工具版本；禁止 agent
  手工清理后直接写 testdata；必要时签名或加 CI attestation。
- **D6 replay/schema 边界**：保持 `expected_reference` 不注入 resolver；
  不改 `expect/v1`；`reference_policy` 等 reference policy 定案后再加；
  合成矩阵不写入 testdata。
- **D7 非目标**：不改生产 reference policy；不新增方法；不修 WSS；不承诺
  finality/canonicality/BFT；不承诺检测 silent stagnation 或 provider tag
  downgrade；不在 quorum 测试中动态改变成员。

### 外援综合评估问题

1. T10 是独立里程碑，还是 T09.1 的扩展？是否包含 T10-B（reference policy
   的 Selection/Confirmation/depth/pinned）？
2. replay 作为 regression oracle 是否可接受？release 的最低 live 门禁是什么？
3. live canary 用 Chainlist 实时列表、pinned snapshot，还是 curated fleet？
   如何固定成员、处理 key/限流、避免动态分母？
4. capture provenance 的最低要求是什么？manifest 足够，还是需要签名/CI
   attestation？
5. “实践可靠”的验收标准是什么？离线全绿 + live 报告 + drift 后重采 +
   real-source closure，是否足够？
6. replay 批次和 live 证据的保留策略是什么？每方法/链一个 canonical 批次，
   旧批次只留 manifest/digest，是否可行？
7. 当前 replay 边界（context 只覆盖 height 改写、expected_reference 只强断言
   height、多轮 confirmation 无 transcript）应纳入 T10，还是另立任务？
8. 哪些失败必须 fail-closed，哪些只能报告？是否接受“uniform stagnation /
   colluding majority / tag downgrade 不可检测”的产品边界？

### 依赖与出口

- T10 是当前优先项；T09.2 可以继续作为本地 regression，但不得作为真实环境
  验证的替代。T09.1 若依赖统一 anchor/business harness，则需等 T10 第一阶段
  范围确认。
- 不把 T10 当作 reference policy 的决策渠道；后者仍需按
  `reference-policy-proposal.md` 比较替代形状后另行立项。
- 第一阶段分析完成前：不改生产 reference policy、不改 `expect/v1` 或
  `batch.json` schema，不把 live 结果直接写入仓库 golden。
- 若评估后立项，出口至少包括：明确的 oracle/门禁定义；anchor 场景矩阵；
  pinned business 覆盖；live canary + provenance 方案；离线全量测试通过；
  real-source 验收报告；非目标与失败边界写清。

## 参考方案（未立项）：Reference Policy 统一

`docs/design/reference-policy-proposal.md` 暂存一个候选：把 reference 拆成
Selection（reported tag + depth / pinned hash）与 Confirmation（统一
majority/floor + hash 确认），`head`/`depth`/`safe`/`finalized` 都成为参数
组合；tag 是 provider 报告，depth 是确认深度，均非 finality。状态为临时
参考方案，**不是决策、不授权实现**；待比较 TargetSelector、pinned-target-only、
两段 evidence、候选规则等替代形状后再决定是否立项。T09.2 在确认前不把
tag 作为固定前提。

## 已拒绝（原则性，不是待办）

- **provider 权重**：默认所有源均不可信，权重会给攻击者一条「不靠伪造、靠塑造
  信誉」的信任杠杆，因此**主动拒绝**，不是「暂未做到」。规则见决策 0058。

## 暂不推进

- Ethereum light client、BLS、sync committee、trusted checkpoint。
- provider **信誉**和 **failure-domain 模型**（独立事项，未决定拒绝，见决策 0058）。
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

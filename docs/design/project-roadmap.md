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
  prepared-request 断言）已落地（`2cd069e`）；组 1 `read-local` 已完成——批次
  `acf3578`（`internal/rpcsample/testdata/read-local/2026-10-19`）加 2026-10-20
  真实三源端到端整合验收（实测状态与契约 §5.1 一致）。剩余 6 个方法待捕获。

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

状态：**进行中**。阶段 0 已完成（`2cd069e`）：`batch.json` 的
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

范围外发现（未修复）：`cmd/soak` 的默认 `-anchor-ttl 1m` 与决策 0026
（禁止跨调用 anchor cache）冲突，直接运行会失败；本次验收显式使用
`-anchor-ttl 0`。该工具的默认值需后续单独修正，不并入捕获批次。

2026-09-26 的形状探查（`internal/rpcsample/probe/`）只是
观察报告，不是 `testdata` 批次，没有 `expect.json`，不解除任何方法，
不得直接升成 golden，不得合成样本。

- 剩余 6 个读方法仍无正式捕获批次：组 2 的 `eth_gasPrice`、
  `eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_syncing`（plan 已备），
  组 3 的 `eth_getLogs`、`eth_feeHistory`（plan 待写）。以
  `TestRegisteredMethodSampleStatus` 的 `noCapture` 名单为准。组 1 的
  `read-local` 5 个方法已完成捕获与真实源整合验收。
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

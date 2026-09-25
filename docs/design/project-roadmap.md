# EthGuard 后续推进规划

本规划记录 T02 之后的方向。`docs/` 已纳入版本管理；moving-tag matrix
的可执行来源仍是测试，文档只作对照。

## 当前基线

- EthGuard 是多源 Ethereum Execution RPC 客户端，不是轻客户端。
- `Corroborated` 表示 provider-backed cross-source evidence，不是 Ethereum
  consensus、canonical proof 或 cryptographic finality。
- `Unknown=0`；错误返回不得携带成功置信状态。
- T02 已完成 EIP-1898 第一切片、tag-aware negative block handling 和支持方法
  的 pending local observation。
- `eth_getLogs` 的 pending 范围现在本地拒绝，避免进入普通 corroboration。
- 类型化 `BlockByNumber(-1)` 现在本地拒绝，不支持 pending block typed projection。
- `eth_getBlockReceipts` 的 pending 本地拒绝覆盖对象形态（大小写字符串、首尾空白、JSON 字符串、`{blockNumber: pending}`）；`ErrInvalidRequest`，不触 anchor、不接触上游。

## T03：Execution RPC 语义收口

1. 已评估：`eth_getLogs` field-level pending observation 保持本地拒绝（T03-A），未实现；如需实现单独立项，不属本轮。
2. `BlockByNumber(-1)` 的 pending typed projection 已在 T03-A 正式保持本地拒绝。
3. EIP-1898 object 形态样本已收录在 `internal/rpcsample/testdata/eip1898-object/2026-09-23`。端点已脱敏。离线测试只重放形状，不把样本写成兼容性结论。
4. `eth_estimateGas`、`eth_createAccessList` 的对象形态已与 T02 五个 state-read 对齐：移动标签对象改写为 `{blockHash}`，显式对象和 pending 透传，非法对象在 anchor 之前本地拒绝。
5. `eth_getLogs` 的 hash / pending / 范围字段保持 T03-A：pending 本地拒绝，`blockHash` 与范围字段互斥。没有改成已支持 pending。
6. `method-contracts.md`、moving-tag matrix 与上述代码一致。matrix 的可执行来源仍是测试；文档只作对照。

## T04：内容与错误兼容性

1. revert data 比较键已区分缺失、null、空字节、非空 hex 与非 hex JSON。样本 `internal/rpcsample/testdata/revert-data/2026-09-23`。测试：`TestNormalizeCallOutcomeSeparatesRevertDataStates`、`TestCallRevertDataConflictIsNotSwallowed`、`TestRevertSamplesUseDistinctDataKeys`、`TestRevertDataSamplesReplayWithoutEndpoints`。
2. block / receipt / transaction 的真实形态在 `internal/rpcsample/testdata/block-receipt-tx/2026-09-23`。白名单外字段不制造分歧，缺必填字段不进入准入，另一块内容绑定失败。测试：`TestRealShapesAgreeAfterPruneAndCaseFold`、`TestMissingRequiredFieldsStayOutOfAdmission`、`TestTypedRealHeaderRejectsAnotherBlocksContent`。
3. wire、admission、content check 与 typed projection 分开。空 uncle 列表和空 receipt 数组保持合法。测试：`TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected`、`TestAdmitDoesNotChangeComparisonKey`、`TestWireNormalizeDoesNotRejectUnknownFields`、`TestTypedReceiptFromAnotherHashIsMalformed`。
4. 同值响应仍只写成 provider-backed evidence。`TestMethodContractsForbiddenClaims` 与 matrix 的 forbiddenClaims 互锁。

## T05：运行期证据与可观测性

1. 已完成。provider-backed reference 的新鲜度和失败语义已写成契约：本次调用内建立，无复用、无 TTL（决策 0026）；`ObservedAt` 是本次建立时刻；失败是 Prepare 期错误，包裹 anchor 原因，业务零 fan-out；证据 `Status=Unknown` 且保留 `Faults`。测试：`TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout`、`TestReferenceEstablishedFreshPerCall`、`TestReferenceFailureEvidenceIsUnknownWithObservedAt`。
2. 已完成。`DecisionEvent` 与 `rpc.Status` 保持双模型，没有把 `failed` 加进置信状态。成功事件串等于 `Status.String()`；conflict、no-valid-response、invalid-request 的事件都是 `failed`，`Conflict` 当且仅当 `ErrConflict`；`Status.String()` 的输出集合恰为 `unknown`、`corroborated`、`selected`、`forwarded`。测试：`TestDecisionEventStatusMatchesSuccessClass`、`TestDecisionEventFailedStatusForClassifiedErrors`、`TestStatusStringSetExcludesFailed`。
3. 已完成。`corroborated` label 迁移记录在 `observe/prometheus` 包注释：status 四取值、告警看 `conflicts_total` 与 `status="failed"`、method/source 基数界。测试：`TestDocumentedDecisionStatusLabelsMatchEmissions`。
4. 已完成。不新增通用 evidence validity helper，见决策 0038。裸状态比较与 `IsCorroborated(err)` 的分工已写明；被拒选项是新增结构化 helper 并迁移全部内部判断点。

## T06：测试流水线收敛与方法合规（计划中）

标准见 `method-verification-pipeline.md`；本节只记录实施顺序、依赖和出口。
`project-roadmap.md` 是唯一持久化计划，不另建执行计划文件。
T06.0、T06.1、Anchor Transport Seam、T06.2、T06.3 和 T06.4 已验收。T06.2、T06.3 和 T06.4 分次提交。T06.4：`eth_call` 按 0039 规则 4e 处理可弃权的 revert data，generic Strict 仍是单 key。T06.5 在这次提交之后开始。

### T06.0 冻结设计约束

依赖：无。状态：2026-09-25 已冻结。出口正文在
`method-verification-pipeline.md` 第 1.1 节；本节只留指针，避免两份规则分叉。

- 0039 规则 4e 采用方案 (i)：返回已佐证的 revert 行为，未佐证的 data 通过删减式投影扣除，只在 Uncertain / 审计证据中保留原始字节。
- dimension evidence 至少区分每个维度的 `Support`、`Reject` 和 `Abstain`，golden 同步记录删减字段。
- `Decision.Value` 允许方法契约声明的删减式投影；禁止新增、替换或跨来源拼装字段。架构记录与 `rpc.Decision` 已按此修订，并标明当前实现仍返回来源原值。
- Policy 组合和真实测试仍是执行事实；不引入 `MethodContract` DSL、代码生成或通用 `suite.RunAll`（决策 0040）。
- `rpc.RPCError` 已有 `Data`。冻结时的缺口是 `rpctest` 的原样 error-body，以及 `observationRPCError` 对缺失 / `null` 的保留。HTTP 上该缺口已由 T06.2 关闭；WSS 仍不能区分缺失和 `null`，限制记在 T06.2。

### T06.1 静态互锁

依赖：T06.0。状态：2026-09-25 已完成。出口：CI 在 matrix 行与 RegisteredMethod 双向检查，并确认 `Tests` 列中的每个完整函数名真实存在。

- `go/parser` 扫描仓库内顶层 `func Test*(t *testing.T)`。名称规则与 Go 的测试发现一致：名字是 `Test`，或 `Test` 之后的首个字符不是小写字母。`TestMain`、方法接收者和 helper 不计入。
- 只检查 `moving-tag-matrix.md` 的 `Tests` 列。`method-contracts.md` 与 roadmap 中的测试名仍是说明性引用。
- `Tests` 列已清理 `Prefix*`、`as above` 和自由文本。不扩展 Markdown 解析语法。
- 列数、非空单元格、catalog 双向覆盖和 forbidden-claims 检查仍在。

### Anchor Transport Seam（T06.2 前置）

依赖：T06.0、T06.1。状态：2026-09-25 已完成。出口：HTTP 与 WSS 共用现有 anchor resolver 和 pool dial，参考语义不按 transport 分叉。

- seam 是 `pool.standardDialer` 加 `collectOne`。resolver 不看 URL scheme。没有第二套 resolver、cache、quorum 或 evidence 类型。说明见 `anchor-transport-seam.md`。
- `latest`、`safe`、`finalized` 不降级。hash 与 height 来自同一次建立。失败留在 Prepare，业务零 fan-out。每次调用重新建立 reference。
- 测试：`TestAnchorTransportReferenceMatchesAcrossHTTPAndWSS`、`TestAnchorTransportFailuresMatchAcrossHTTPAndWSS`、`TestAnchorTransportReestablishesEachCall`。进程内 HTTP/WSS，无外部网络。

### T06.2 Replay 基础设施

依赖：T06.0，以及已完成的 Anchor Transport Seam。状态：2026-09-25 已验收，已提交。出口已满足：同一批样本可以通过 Policy 层和 Aggregator 层重放。没有新增 `expect.json`。

- 已提取 `internal/rpcsample/replay`。`LoadBatch` 按记录文件的原始字节校验 sha256，篡改单字节即失败。`BodyOf` 只还原响应体，不做裁决。
- Policy 层用真实 `CallPolicy`、`TransactionByHashPolicy` 和 `ChainIDPolicy` 构造 `rpc.Observation`。错误响应保留缺失、JSON `null`、`0x` 和具体 data，不把缺失与 `0x` 合成同一个比较值。
- 端到端层经 `rpctest.FailWithData` / `ReturnRaw` 返回记录体，走 `Aggregator.Aggregate`。`tx-one` 覆盖真实 Admit；单侧成功在 `RequireCorroborated` 下覆盖 typed-read 门禁（0038）。
- `latest` 使用进程内 anchor。`eth_getBlockByNumber` 仍请求 `latest`，`eth_call` 改写到本次 hash。无外部网络。
- 已接受的范围例外：geth 把 error data 解成 `interface{}`，缺失和 JSON `null` 在进入 `observationRPCError` 之前都变成 nil。HTTP/HTTPS 因此在 `pool/rawbody.go` 增加 capturing transport，`pool/pool.go` 的 `standardDialer` 和 `pool/collect.go` 的 `collectOne` 保留原始 data。`preservedDataError` 只暴露 `ErrorDataJSON`，不实现 geth `DataError`，`RevertErrorData` 仍落到内部 geth 错误。只包 `http`/`https`；WSS 仍走原来的 `ethclient.DialContext`。这不是第二套 resolver、cache、quorum 或 evidence。
- WSS 没有原始帧，缺失与 `null` 仍坍缩。四态只在 HTTP 上成立。`rpctest` 仍没有 WSS 节点。
- 旧 `*_sample_test.go` 的本地 sha256 loader 在 T06.2 结束时尚未删除。T06.3 已停用该入口，样本读取只经 `replay.LoadBatch`。
- `safe` / `finalized` 矩阵本轮未重跑，留到 T06.5 方法批次前。business 零 fan-out 未单列重验；anchor transport 全量测试仍绿。T06.3 若改 anchor 或 pool 错误路径，必须重跑那一组。
- 证明测试：`TestSameFixtureReplaysThroughPolicyAndAggregator`、`TestReplayErrorDataStatesStayDistinct`、`TestReplayTypedReadGateRejectsUncorroboratedCall`、`TestReplayChainIDAgreesWithoutAnchor`、`TestLoadBatchRejectsChangedRecordBytes`。样本 record 与 sha256 未变。

### T06.3 Golden 快照与批次归档

依赖：T06.2。状态：2026-09-25 已验收，已提交。T06.2 的双层重放出口已满足。出口：CI 能从批次目录重放并比较 golden，变更必须产生可审阅 diff。本提交不开始 T06.4，也不再改 `expect.json`。

已落地，尚未验收：

- `revert-data`、`block-receipt-tx`、`eip1898-object` 原样迁入 `testdata/<feature>/2026-09-23/`。`index.json` 与 record 字节、sha256 未改。每批新增 `batch.json`。`web3_clientVersion` 与区块高度都没有采集，记为 null，只说明缺上下文，不作 provider / client 归因。
- 首个 golden 是 `internal/rpcsample/testdata/revert-data/2026-09-23/expect.json`（`ethguard.expect/v1`）。它钉住 request、majority、floor 2、N=2、每源比较键、准入 Faults、execution 维度的 Support / Reject / Abstain、删减字段、Decision / error，以及 `transport: http`。缺失与 `0x` 仍是不同键，短簇是 conflict。Abstain 为空，reductive 为 false，因为没有做 0039。
- `block-receipt-tx` 与 `eip1898-object` 还没有 `expect.json`。不得用这份 HTTP golden 断言 WSS。
- 旧样本测试不再自带 sha256 loader。`TestRevertDataBatchMatchesExpect` 走 Policy 与 Aggregator，并在 Corroborated 结果上再过 typed-read 门禁。`TestExpectTamperProducesReviewableDiff` 证明篡改会打出 diff。`TestExpectTransportRejectsWSS` 拒绝把 HTTP golden 用到 WSS。`TestBatchLayoutKeepsIndexAndOmitsLaterGoldens` 钉住三份 `batch.json` 的 null 上下文，并确认另外两批没有 `expect.json`。
- 读取器已停用本地 sha256 loader，只经 `replay.LoadBatch`：`content_boundary_test.go`、`rpc/methods/content_compat_test.go`、`rpc/methods/eth_call_revert_test.go`、`internal/rpcsample/revert_data_sample_test.go`、`internal/rpcsample/eip1898_object_sample_test.go`。后两份改为 `package rpcsample_test`。`rejectURL` 脱敏检查保留。
- 文档路径改到 `.../2026-09-23`：`docs/decisions.md`、`docs/design/method-contracts.md`、`docs/design/method-verification-pipeline.md` 第 1.1 节第 6 条，以及本文件 T03/T04 的样本路径。`docs/design/moving-tag-matrix.md` 同步这些路径，eth_call 的 `Tests` 列新增 `TestRevertDataBatchMatchesExpect`。
- 本轮没有改 anchor 或 pool 的错误路径，所以没有重跑 transport 三测试。

约束仍是：

- 按 `testdata/<feature-or-method>/<batch-id>/` 组织批次；每批次保留 `index.json`，不删除 sha256 凭据。record 字节与 sha256 不变。
- `expect.json` 固定 schema 版本、request、mode、floor、成员数 N、每源比较键、准入 Faults、维度证据、删减字段、最终 Decision / error，以及 `transport`。data presence 四态只对 `transport: http` 成立。不得用 HTTP golden 断言 WSS；WSS 上缺失与 `null` 的坍缩是已知缺口。
- 迁移前冻结旧 `*_sample_test.go` loader，新回放只经 `replay.LoadBatch`，避免 sha256 双轨。
- golden 是数据格式，不包含条件、继承、通配或生成测试逻辑；禁止盲目 `-update`。
- 采集时尽力记录 `web3_clientVersion`，只作上下文，不能用于 provider / client 归因。
- 不做 0039 规则 4e，不新增 RegisteredMethod，不把缺失与 `0x` 并成同一个比较值。
- 若改动 anchor 或 pool 的错误路径，重跑 `TestAnchorTransportReferenceMatchesAcrossHTTPAndWSS`、`TestAnchorTransportFailuresMatchAcrossHTTPAndWSS` 和 `TestAnchorTransportReestablishesEachCall`。

提交划分（已执行，先 T06.2，后 T06.3）：

两次提交分开，没有一锅端。T06.2 那一次的 `batchDir` 指向迁移前的 `testdata/<feature>/`。本提交把 `batchDir` 改回 `2026-09-23`，并带上批次与 golden。

第一次，T06.2。整文件：`aggregator/aggregator.go`、`pool/collect.go`、`pool/pool.go`、`pool/rawbody.go`、`rpctest/node.go`、`rpctest/data_test.go`、`internal/rpcsample/replay/replay.go`。`replay_test.go` 只留 `TestLoadBatchVerifiesRecordSHA256`、`TestLoadBatchRejectsChangedRecordBytes` 和 `copyTree`，`batchDir` 指向迁移前目录；不含 `TestBatchLayoutKeepsIndexAndOmitsLaterGoldens` 和 `batchMeta`。`layers_test.go` 留四个 replay 测试，`batchDir` 同样指向迁移前目录。`project-roadmap.md` 只带 T06.2 状态、T06.0 的 HTTP 缺口句、T06.2 正文（loader 句停在“尚未删除”）和顺序图的 T06.2 行。`method-verification-pipeline.md` 带事实第 4、5 条，以及第 6 条里“replay 按原始字节校验 sha256、当时尚无 expect.json”的一半；端到端要求里的 `FailWithData`、HTTP 保留和 WSS 不能作四态也在这次。不进入这次：`expect_test.go`、`expect.json`、三份 `batch.json`、113 个改名、五个读取器、`docs/decisions.md`、`method-contracts.md`、`moving-tag-matrix.md`，以及本文件里 T03/T04 的 `/2026-09-23`、T06.3 正文和“现有样本已迁入”。

第二次，T06.3。113 个 `index.json` / record 纯改名（字节与 sha256 不变），三份 `batch.json`，`revert-data/2026-09-23/expect.json`，`expect_test.go`。`replay_test.go` 补批次布局测试并把 `batchDir` 改到 `2026-09-23`；`layers_test.go` 只改 `batchDir`。读取器：`content_boundary_test.go`、`rpc/methods/content_compat_test.go`、`rpc/methods/eth_call_revert_test.go`、`internal/rpcsample/revert_data_sample_test.go`、`internal/rpcsample/eip1898_object_sample_test.go`。文档：`docs/decisions.md`、`method-contracts.md`、pipeline 第 6 条的批次与 golden 句、`moving-tag-matrix.md` 的路径和 eth_call `Tests` 列，以及本文件剩余 hunk。提交说明必须点名这些读取器和文档，不能只写批次与 golden。

### T06.4 决策 0039 落地

依赖：T06.0、T06.2、T06.3。出口：4e 的所有代表性矩阵和端到端调用通过，文档不再描述旧的扁平键结果。状态：2026-09-25 已验收，已提交。

入口已满足：T06.3 已提交。本步改了 `expect.json`，Abstain 与 reductive 的变化在 diff 里。generic Strict 仍是单 key。缺失与 `0x` 仍是不同比较键：缺失是 data 弃权，不是空字节。

落地范围：

- 在 `eth_call` 上声明 revert `data` 可弃权，由方法专属多维 resolver 处理；generic Strict 保持单 key 语义。
- 弃权源留在投票分母，不进 data 的 Reject，不用其他来源的值补全。
- 短 data 簇采用删减式投影；typed `CallContract` 返回无 data 的 corroborated revert，原始具体 data 仅经 Uncertain / 审计证据可见。
- 覆盖 N=2、N=3、N=4、全 abstain、具体 data 达到 quorum、具体 data 冲突和 unanimity 模式。
- 更新 `method-contracts.md` 0.5、matrix 的 eth_call 行、相关现状测试及 golden。

### T06.5 扩展读方法批次

依赖：T06.1–T06.4。Anchor Transport Seam 已完成，不再单独阻塞本步。出口：每个新增 RegisteredMethod 通过标准第 5 节清单，并提交对应样本状态和 golden。

- 按方法逐项增加 matrix、Policy 测试、admission / normalization 边界、端到端 replay 和 golden。
- 不为批量扩展新增声明式方法契约或统一生成器；重复行为通过普通 Go helper 和 table-driven tests 复用。

### T06 执行顺序与审计门禁

`project-roadmap.md` 是唯一计划源。`project-roadmap-next-map.md` 已删除，不再使用编号任务地图。

```text
T06.0 设计不变量          已完成
T06.1 matrix 互锁         已完成
Anchor Transport Seam     已完成，T06.2 的前置
        |
        v
T06.2 replay              已验收，已提交
        |
        v
T06.3 golden 与批次归档   已验收，已提交：三批已归档，仅 revert-data 有 expect
        |
        v
T06.4 0039 规则 4e        已验收，已提交
        |
        v
T06.5 读方法批次扩展
```

现有样本已迁入各自 `2026-09-23` 批次。不得新增读方法。T06.4 没有把缺失与 `0x` 收成同一个比较值：缺失是弃权，`0x` 仍是 data 断言。T06.2、T06.3 与 T06.4 已分次提交。T06.4 提交后 T06.5 才开始。每个任务先提交自己的验收证据，再解除后继任务。

#### 执行者通用要求

1. 只修改任务范围内的代码、测试、样本和文档；发现范围外问题，单独记录，不顺手修复。
2. 不引入 `MethodContract` DSL、代码生成、`suite.RunAll`、新的 quorum 模式或 provider 权重。
3. 不通过删减投票分母、降低 floor、改变 typed gate 或自动补字段来让测试变绿。
4. 所有 replay 必须离线运行；允许进程内 `httptest`，禁止外部网络 I/O。
5. 每个任务提交：变更摘要、运行命令、测试结果、样本 / golden 变化、未解决风险和下一任务解除依据。

#### 审计拒收条件

- 只测试 Policy 层而没有 Aggregator / typed-read 端到端证据；
- 用 mock 结果替代生产路径的 JSON-RPC error、Admission 或 anchor；
- 新增第二份方法契约事实源，或以通配 / `as above` 扩展 Markdown 语法；
- 修改样本 record 却没有对应 sha256 / index / golden 变化说明；
- 将 provider-backed evidence 写成 canonical、finality、consensus 或链上正确性结论；
- 任务声称完成但没有可复现的 `go test` 命令和失败路径验证。

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
6. 对外说明它提供的是 corroboration evidence，而非 Ethereum consensus proof。

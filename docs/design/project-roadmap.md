# EthGuard 后续推进规划

本规划记录 T02 之后的方向。`docs/` 已纳入版本管理；moving-tag matrix
的可执行来源仍是测试，文档只作对照。

当前优先事项是返回值框架。规则在 `return-value-design.md`，任务和出口只写在下面那一节。未捕获读方法的批次排在它后面，不并进返回值任务。

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
T06.0、T06.1、Anchor Transport Seam、T06.2、T06.3、T06.4 和 T06.5 已验收。T06.2–T06.5 分次提交。T06.4：`eth_call` 按 0039 规则 4e 处理可弃权的 revert data，generic Strict 仍是单 key。T06.5 已验收，已提交：两份缺 golden 的捕获批次已补上 `expect.json`，没有合成未捕获方法的样本。

### T06.0 冻结设计约束

依赖：无。状态：2026-09-25 已冻结。出口正文在
`method-verification-pipeline.md` 第 1.1 节；本节只留指针，避免两份规则分叉。

- 0039 规则 4e 采用方案 (i)：返回已佐证的 revert 行为，未佐证的 data 通过删减式投影扣除，只在 Uncertain / 审计证据中保留原始字节。
- dimension evidence 至少区分每个维度的 `Support`、`Reject` 和 `Abstain`，golden 同步记录删减字段。
- `Decision.Value` 允许方法契约声明的删减式投影；禁止新增、替换或跨来源拼装字段。架构记录与 `rpc.Decision` 已按此修订：`eth_call` 的 revert `data` 已投影。未迁移且未声明投影的方法仍返回来源原值。已迁移的数量和成功 DATA 写规范编码。`eth_feeHistory` 写受保护字段的规范对象，比较键仍是 feeHistory 键。
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
- `safe` / `finalized` 由 tag-aware anchor 分别建立，已由 T06.5 的捕获 golden 覆盖；business 零 fan-out 由 anchor transport 测试锁定。若改 anchor 或 pool 错误路径，必须重跑那一组。
- 证明测试：`TestSameFixtureReplaysThroughPolicyAndAggregator`、`TestReplayErrorDataStatesStayDistinct`、`TestReplayTypedReadGateRejectsUncorroboratedCall`、`TestReplayChainIDAgreesWithoutAnchor`、`TestLoadBatchRejectsChangedRecordBytes`。样本 record 与 sha256 未变。

### T06.3 Golden 快照与批次归档

依赖：T06.2。状态：2026-09-25 已验收，已提交。出口：CI 能从批次目录重放并比较 golden，变更产生可审阅 diff。

- 三批已归档 `testdata/<feature>/2026-09-23/`，record 字节/sha256/`index.json` 未动，各一份 `batch.json`（`web3_clientVersion` 与高度缺上下文，记 null）。
- 首个 golden 为 revert-data `expect.json`；另两批的 `expect.json` 由 T06.5 补齐，见 T06.5。读取器只经 `replay.LoadBatch`。
- golden 为数据格式，禁条件/继承/通配/生成逻辑与盲目 `-update`；改 anchor/pool 错误路径重跑 transport 三测试。

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

依赖：T06.1–T06.4。Anchor Transport Seam 已完成，不再单独阻塞本步。出口：每个新增 RegisteredMethod 通过标准第 5 节清单，并提交对应样本状态和 golden。状态：2026-09-25 已验收，已提交。

- `block-receipt-tx/2026-09-23/expect.json` 与 `eip1898-object/2026-09-23/expect.json` 是 `ethguard.expect/v1`，`transport: http`。record 字节、sha256 和 `index.json` 未改。`revert-data` 的 6 个 case 未改。
- 回放走真实 Policy、`Aggregator.Aggregate` 和 typed-read 门禁。`safe` / `finalized` 由 tag-aware anchor 分别钉住，不脚本成 `latest`。pending 保持 Selected，同意也不会变成 Corroborated。空 body 按记录里的 429 或 403 作为 transport fault。
- generic Strict 仍是单 key：golden 记一个 `value` 维度，Abstain 为空，reductive 为 false。`eth_call` 仍只有自己的 execution / data 维度。
- `storage-number-object` 的 s4 请求与其他来源不同，且 body 为空。golden 把它记为 transport fault，并多标 `request_differs`，不拿它的请求去投票。
- 没有捕获批次的读方法不新增合成样本：`eth_getLogs`、`eth_feeHistory`、`eth_chainId`、`net_version`、`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_blockNumber`、`eth_syncing`、`net_peerCount`、`net_listening`。`TestRegisteredMethodSampleStatus` 钉住这份名单。
- 不为批量扩展新增声明式方法契约或统一生成器；重复行为通过普通 Go helper 和 table-driven tests 复用。
- `TestBatchLayoutKeepsIndexAndOmitsLaterGoldens` 改名为 `TestBatchLayoutKeepsIndexAndExpect`。三批现在都有 `expect.json`。

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
T06.3 golden 与批次归档   已验收，已提交：三批已归档，expect 见 T06.3/T06.5
        |
        v
T06.4 0039 规则 4e        已验收，已提交
        |
        v
T06.5 读方法批次扩展      已验收，已提交：block-receipt-tx 与 eip1898-object 有 expect
```

现有样本已迁入各自 `2026-09-23` 批次。不得新增读方法。T06.4 没有把缺失与 `0x` 收成同一个比较值：缺失是弃权，`0x` 仍是 data 断言。T06.2、T06.3 与 T06.4 已分次提交。T06.4 与 T06.5 已分次提交。未捕获的读方法仍然没有 golden。每个任务先提交自己的验收证据，再解除后继任务。

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


## 返回值框架（当前优先）

规则只在 `return-value-design.md`。本节约执行顺序、范围和出口，不另写字段标准，也不把第 14 节展开成字段表。2026-09-27 起这一节优先于未捕获读方法的批次。

一次只开工一个已解除的任务。后一个任务在前一个出口核对通过之前不得改代码。每个任务单独提交。提交说明写变更摘要、`go test` 命令、失败路径、样本或 golden 变化、未解决风险。

这些任务都不做：规范编码提前进 RV0、0041 提前于 RV0 出口、阶段 3a/3b 的实现、`from` 进域、给 Provenance 补字节上限、合成样本、把 `internal/rpcsample/probe/` 升成 golden 批次。BLS、sync committee、checkpoint 仍见暂不推进。

不挡住 RV0 的开放项只有两项。`from` 在 CPU 测量之后要不要定义成派生字段，默认留在域外；没有测量不能进域，也不能为此把整票判掉。Provenance 截断上限没有数字，不在任务里补。

已知缺口不能写成已经语义安全：未迁移对象的 Value 仍是代表源原文（4e 删减除外），`from` 和 `size` 还会出现在这些 Corroborated 结果里。数量和成功 DATA 的 Value 是规范编码，`eth_feeHistory` 的 Value 是受保护字段的规范对象。这不表示区块已经语义安全。`eth_feeHistory` 的键外字段这一刀不进 Value，也没有 Extensions 或 Provenance 可去，这个缺口还开着。

```text
RV0 代表源、同源、锚点 hash    已验收：8aac099
        |
        v
RV1 数量与 DATA 规范编码        已验收：48e2c64
        |
        v
RV2a eth_feeHistory            已验收：dee2208
        |
        v
RV2b1 eth_getLogs              当前，可开工。先写 0043，再编码
        |
        v
RV2b2 receipt、transaction、blockReceipts、accessList
                               未解除：from 与承诺内容尚未逐项判定
        |
        v
RV3a eth_call 信封             方向已锁，未解除
RV3b 区块重编码                方向已锁，未解除；from 仍开放
        |
        v
RV4 已注册方法不再是 raw        未解除
```

### RV0 代表源、同源、锚点 hash

状态：已验收（8aac099）。依据是 `return-value-design.md` 第 7 节和第 11 节阶段 0。没有引入编码器，没有立 0041，没有改「Value 取哪一簇」。Value 仍是代表源原始载荷。

出口：锚点拼写用例通过；Value、Evidence、Withheld 同源用例通过。出口文字必须写明 Value 仍是代表源原始载荷。

一次提交，只含下面这些改动：

1. Strict 的 `cluster` 不再用后写入 map 的那一份。获胜簇 Value 改为本次捕获顺序的第一个支持源的原始载荷。Evidence 只取这一人；这一人没有 evidence 就保持空，不得改用簇内后一个来源。
2. CallRevert 的 `representative` 从簇内最后一个改为捕获顺序第一个。4e 仍按现实现选簇：data 有具体值时用 data 排名第一的簇；该簇未达门槛时仍用它的信封再删 data；全部在 data 上弃权时才用执行簇。Withheld 只放这一人扣下的 data。
3. Median 保持「与中位数值相等的样本中的第一个」。Strict、CallRevert、Median 共用一个代表源函数。带出获胜簇 Value 时写入 `Decision.Representative`，失败但仍带出该 Value 的路径也填。Local、Forward、`ResolveLocal` 保持零值。
4. `Decision.Representative` 用现有 `SourceRef`，放在 Evidence 旁。
5. `StrictHeadAnchor` 经 `blockRefFromDecision` 发出的区块 hash 改为小写，与 `FinalizedHeadAnchor` 已有的小写一致。这是发给来源的请求参数，不是返回值规范化。不改比较键，也不改区块 `Decision.Value` 里的来源拼写。
6. 同一提交只改 `method-contracts.md` 两处，不能先改文档：第 0 节「获胜簇的 Value 取簇内最后一个池序成员的原始回答」，以及 byNumber 归一化行「获胜簇 Value 取簇内最后池序成员的原始对象」。都改为捕获顺序第一个。Value 仍写明是该来源的原始回答，不是规范编码。

不改：`docs/decisions.md`、`method-verification-pipeline.md`、本文件的任务定义、`projection.StandardFields`、样本 record 字节、编码器、`rpc/result.go`、`wire/encode.go`、Provenance、Extensions 指纹。不缩小分母，不降低门槛，不把 Median 或 Local 的 Selected 升成 Corroborated。

既有三批 `expect.json` 不存 Value 字节，`projectionMatches` 只要求 Value 是某个来源的载荷，所以旧 golden 发现不了「最后一人」。RV0 必须另写测试，用两份同键不同拼写证明选中的是捕获顺序第一人，并且 Evidence、Withheld 是同一人。测试不得断言「换位置则 Value 字节不变」。

replay 仍以 `index.json` 的来源顺序为捕获顺序。三批 `expect.json` 的 `decision` 增加代表源 `id` 与 `ordinal`，用来钉住第一人。不增加规范 Value、Provenance 或 result 列。status、error、deleted fields 除代表源身份外保持不动。禁止盲目 `-update`。record 字节、sha256、`index.json` 不改。

规划在把本任务标成已验收之前核对 diff：

- 改动落在代表源选择、同源、`Representative`、锚点请求 hash 小写、上述两处契约句子、证明这些行为的测试，以及因此必须改的既有断言和三批 representative 字段。
- 没有编码器，没有 0041，没有第二份字段表，没有把 `from` 或 `size` 从 Value 去掉。
- 锚点测试失败路径是：获胜头 hash 为 `0X` 时，发出的请求参数必须是小写；对应区块 Value 仍保留来源原文里的 `0X`。
- 同源测试失败路径是：输入 `[a, b]` 且两人同键时，Value、Evidence、Withheld 都来自 a，而不是 Value 取 b、Evidence 取 a。
- 提交说明里有可复现的 `go test` 命令。范围外问题只记录，不顺手修。

### RV1 数量与 DATA 规范编码

状态：已验收（48e2c64）。0041 只覆盖数量和成功 DATA。已迁移标量的比较键就是规范 JSON 字节。`eth_feeHistory`、收据、交易、日志、access list、区块和 `eth_call` 信封都不在这一刀。

### RV2a eth_feeHistory

状态：已验收（dee2208）。0042 只覆盖 `eth_feeHistory`。比较键仍是 feeHistory 键。收据、交易、日志、access list、区块和 `eth_call` 信封都不在这一刀。

这一刀只做 `eth_feeHistory`。第 5 节已经锁住比例的十进制规则，第 6.2 节锁住它的比较键：仍是方法现有的 feeHistory 键，不是整段规范 JSON；键里的 `gasUsedRatio` 必须是规范十进制，不得再经 `[]float64` 把不同原值折成同一键。Value 是受保护字段的规范对象。

一次提交。先写 0042，再改代码。0042 只采纳这一方法，不把 0041 改成对象决策。

受保护字段只有比较键里已有的四个：`oldestBlock`、`baseFeePerGas`、`gasUsedRatio`、`reward`。依据是第 4.2 节第一步。键外字段不进 Value。不要做 Extensions 差集，不要做 Provenance；被留下的键外字段这一刀无处安放，提交说明里写明这个缺口，不要写成已经保留。

顺序沿用第 6.1 节：逐源 EncodeCheck，失败记 Fault，留在分母，不投票；再用通过者的 feeHistory 键做唯一一次 Resolve；最后 EncodeCommit。簇内规范对象字节不同则是 Unknown，`ErrMalformedResponse`，原因含 `result encoding disagrees within cluster`。不第二次投票。

和 RV1 标量的差别必须保住。标量的分簇键仍是规范 JSON，回归由 `TestMigratedQuantityTieUsesCanonicalKey` 守住。`eth_feeHistory` 不得走那条「有编码器就把键换成规范 JSON」的分支。比较键继续来自 Normalize；Value 来自编码器，不从键反序列化再拼对象。

比例按第 5 节，用 `json.Number` 读十进制原文，输出 `strconv.FormatFloat(f, 'f', -1, 64)`：

- `0.50`、`0.5000` 和 `0.5` 同键，Value 里写 `0.5`。`0.10` 与 `0.1`、`1.0` 与 `1` 同样处理。
- `1e-1` 与 `0.1` 同键，输出 `0.1`。
- `0.30000000000000001` 的最短形式是 `0.3`，但十进制原值变了。EncodeCheck 失败，记 Fault，留在分母，不投票。它不是 `0.3` 的另一种拼法。
- 超出 `[0,1]`、NaN、Inf 仍是 Admit 无效响应，不改成新的编码类别。

数量仍用现有 QUANTITY 规则。已经拒绝前导零的继续拒绝。比较键里的数量可以保持今天的十进制投影。Value 里的 `oldestBlock`、`baseFeePerGas`、`reward` 写成规范 QUANTITY：`0x` 加小写十六进制，无前导零，零写作 `0x0`。对象用编码器 struct 的字段顺序、紧凑 JSON、`SetEscapeHTML(false)`。`reward` 缺失则省略，不补成 null 或空数组。

result 列只有 `eth_feeHistory` 改为 canonical。Representative 仍是捕获顺序第一人，不决定 Value 拼写。pending 的 `ResolveLocal` 可以编码被选中的那一份，Representative 保持零值，Status 仍是 Selected。

新文件只允许 `rpc/internal/objects/feehistory/encode.go`。不把通用对象编码器放进 `wire/encode.go`，不建编码器注册表。

测试至少覆盖：`0.5` 与 `0.50` 同簇且换序不改变 Value；上述不可规范化比例记 Fault 且分母不缩小；标量平票仍按规范 JSON 决胜。没有 feeHistory 的 `expect.json`。禁止合成样本，禁止把 `internal/rpcsample/probe/` 升成 golden，禁止 `-update` 已有三批 expect。record 字节、sha256、`index.json` 不改。

不改：receipt、transaction、logs、blockReceipts、accessList、区块重编码、`eth_call` 的 code 与 message、`from` 和 `size` 移出、Extensions、Provenance、分母、门槛、Median 或 Local 升为 Corroborated。未跟踪的 `cmd/shapeprobe/`、`internal/shapeprobe/`、`internal/rpcsample/probe/` 不读不改。

文档只改现在成为事实的句子。不要写收据已经规范编码，不要写 byHash 已由承诺约束，不要写 0039 第 2 类已离开区块 Value，不要写原文进了 Provenance。执行者不得把本任务标成已验收。

出口：`go test ./... -count=1`。失败路径是不可规范化比例被记为 Fault，并且不能靠丢掉该来源把分母缩小后变成成功。

### RV2b1 eth_getLogs

状态：可开工。RV2a 已在 dee2208 验收。RV2b2、RV3a、RV3b、RV4 仍不实现。

这一刀只做 `eth_getLogs`。比较键仍是现有的 `normalizeLogs` / `PruneMethod` 结果，不是另写的整段 JSON。Value 是掩码留下的日志字段的规范编码。依据是第 4.2 节第一步：比较键覆盖的字段进域。不要抄 `projection.LogsStandardFields`，也不要另列一张字段表。

一次提交。先写 0043，再改代码。0043 只采纳这一方法，不改 0041 和 0042。

受保护字段只限于现有 `eth_getLogs` 掩码会留下的日志字段：`address`、`topics`、`data`、`blockNumber`、`transactionHash`、`transactionIndex`、`blockHash`、`logIndex`、`removed`，以及掩码里已有的可选 `blockTimestamp`。掩码丢掉的字段不进 Value。`log.Validate` 今天要求的字段仍然必填，缺了就是 EncodeCheck 失败。`blockTimestamp` 没有时省略，不补 null。空数组仍是合法结果 `[]`。JSON null 仍是非法回答，不是空数组。

顺序沿用第 6.1 节：逐源 EncodeCheck，失败记 Fault，留在分母，不投票；再用现有日志键做唯一一次 Resolve；最后 EncodeCommit。簇内规范字节不同则是 Unknown，`ErrMalformedResponse`，原因含 `result encoding disagrees within cluster`。不第二次投票。

标量和 `eth_feeHistory` 的分簇键都不得改。日志走已有的 `WithCanonicalObject`：有编码器时键仍来自 Normalize。不要把日志键换成规范 JSON。Value 来自编码器，不从比较键反序列化，也不要把 JSON 解进 `any` 或 `float64`。

规范形式沿用第 5 节和已经落地的标量规则：地址和哈希小写；数量是 `0x` 加小写十六进制，无前导零，零写作 `0x0`；`data` 是规范 DATA，前导零保留；`removed` 是 JSON 布尔。数组顺序就是响应顺序，不得排序。对象用编码器 struct 的字段顺序、紧凑 JSON、`SetEscapeHTML(false)`。

result 列只有 `eth_getLogs` 改为 canonical。Representative 仍是捕获顺序第一人，不决定 Value 拼写。pending 仍是 Prepare 里的 `ErrInvalidRequest`，不要加本地观测，也不要编码 pending。

新文件只允许 `rpc/internal/objects/log/encode.go`。不把通用对象编码器放进 `wire/encode.go`，不建编码器注册表。

测试至少覆盖：同一批日志只差大小写或键序时同簇，换序不改变 Value，Representative 跟随第一人；掩码外字段不进 Value，也不把两人拆开；缺必填字段记 Fault 且分母不缩小；标量平票和 feeHistory 的 `0.5`/`0.50` 用例仍通过。没有 `eth_getLogs` 的 `expect.json`。禁止合成样本，禁止把探针升成 golden，禁止 `-update` 已有三批 expect。

不改：receipt、transaction、blockReceipts、accessList、区块重编码、`eth_call` 的 code 与 message、`from` 和 `size`、Extensions、Provenance、分母、门槛。未跟踪的 `cmd/shapeprobe/`、`internal/shapeprobe/`、`internal/rpcsample/probe/` 不读不改。

键外字段这一刀仍无处安放。提交说明写明这个缺口，不要写成已经保留，也不要提前做 Extensions。文档只改现在成为事实的句子。执行者不得把本任务标成已验收。

出口：`go test ./... -count=1`。失败路径是缺必填字段的来源留在分母里，不能靠丢掉它变成成功。

### RV2b2 及以后

状态：未解除。

RV2b2：receipt、transaction、blockReceipts、accessList。`from` 仍默认留在域外，交易和收据的承诺内容还没有逐项走过第 4.2 节，所以这一段不编码。Extensions 改为差集也留在这里。

RV3a：`eth_call` 的 code 与 message 留在代表源，不进受保护域，不合成信封。RV3b：区块 Value 只重编码已经核对过承诺的内容。两段方向已锁，都没有开工出口。RV4：方法规则索引断言已注册方法没有 `raw`。

## 排后：未捕获读方法的捕获批次

状态：排在返回值框架之后。2026-09-25 的「未采集」已过时：`internal/rpcsample/probe/mainnet/2026-09-26/` 有一次形状探查，报告只陈述观察，不判断如何处理。它不是 `testdata` 批次，没有 `expect.json`，不解除 `eth_chainId`，也不改变 `TestRegisteredMethodSampleStatus` 钉住的名单。返回值任务不得把这批探针升成 golden，不得合成样本。

- 11 个读方法仍无正式捕获批次：`eth_getLogs`、`eth_feeHistory`、`eth_chainId`、`net_version`、`eth_gasPrice`、`eth_maxPriorityFeePerGas`、`eth_blobBaseFee`、`eth_blockNumber`、`eth_syncing`、`net_peerCount`、`net_listening`。
- 契约、matrix 行和既有 httptest 仍在。补批次时仍须满足六项进入条件，`safe`/`finalized` 各自锚定，不落到 `latest`。
- `balance-safe-canonical` 本批捕获达不到 quorum，补采达标批次前保持冲突结论，不降 floor。
- 采集时尽力记录 `web3_clientVersion` 与区块高度，只作上下文，不作归因。`transport: http`；WSS 缺失/`null` 坍缩缺口继续携带。已验收三批 golden 的 status、error 和 deleted fields 不因补批次而改写。

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

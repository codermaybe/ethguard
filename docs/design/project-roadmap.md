# EthGuard 后续推进规划

本规划记录 T02 之后的方向。`docs/` 已纳入版本管理；moving-tag matrix
的可执行来源仍是测试，文档只作对照。

返回值框架已推进到键值合一（0047）与证据通道收缩（0048）。规则在 `return-value-design.md`，任务和出口只写在下面那一节；之后的顺序见该节末尾的「后续规划」。标准驱动的自动兼容闭环是返回值框架之后的下一项主线；未捕获读方法的批次排在它后面，不并进返回值任务。

本路线图新增一条总约束：对外提供的值必须是方法契约定义的**聚合结论**。
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
- `Decision.Value` 允许方法契约声明的删减式投影；禁止新增、替换或跨来源拼装字段。架构记录与 `rpc.Decision` 已按此修订：`eth_call` 的 revert `data` 已投影。未迁移且未声明投影的方法仍返回来源原值。已迁移的数量和成功 DATA 写规范编码。`eth_feeHistory` 写受保护字段的规范对象。已迁移对象方法的比较键按 0047 也是规范编码，即编码器的输出；刻意保留窄键的方法（区块重编码）维持自己的键。
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

本节各任务的状态行与正文记录的是各自验收当时的比较键。`eth_feeHistory`、`eth_getLogs`、`eth_createAccessList`、`eth_getTransactionReceipt`、`eth_getBlockReceipts` 五个方法的比较键已由 0047 改为规范编码，即编码器的输出；正文里「比较键仍是……键」的句子是当时的验收事实，按仓库惯例不改写，只以本行与各任务的行内指向为准。

一次只开工一个已解除的任务。后一个任务在前一个出口核对通过之前不得改代码。每个任务单独提交。提交说明写变更摘要、`go test` 命令、失败路径、样本或 golden 变化、未解决风险。

这些任务都不做：规范编码提前进 RV0、0041 提前于 RV0 出口、阶段 3a/3b 的实现、`from` 进域、合成样本、把 `internal/rpcsample/probe/` 升成 golden 批次。BLS、sync committee、checkpoint 仍见暂不推进。

不挡住 RV0 的开放项只有一项。`from` 在 CPU 测量之后要不要定义成派生字段，默认留在域外；没有测量不能进域，也不能为此把整票判掉。原先的「Provenance 截断上限」随代表源 Raw 一起撤销（0048）。

已知缺口不能写成已经语义安全：未迁移对象的 Value 仍是代表源原文（4e 删减除外），`from` 和 `size` 还会出现在这些 Corroborated 结果里。它们是迁移期间的已知缺口，不是目标形态，也不能成为后续方法的实现模板。数量和成功 DATA 的 Value 是规范编码，`eth_feeHistory` 的 Value 是受保护字段的规范对象。这不表示区块已经语义安全。`eth_feeHistory` 的键外字段这一刀不进 Value，也没有 Extensions 或 Provenance 可去，这个缺口还开着。

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
RV2b1 eth_getLogs              已验收：f8b4280
        |
        v
RV2b2 eth_createAccessList     已验收：2026-09-30，实现 cade5a3；验收修复见本节
        |
        v
RV2b3a receipt                 已验收：2026-09-30，实现 2583c28；0045 排除 from
        |
        v
RV2b3c blockReceipts           已验收：2026-09-30；0046 复用收据编码器
RV2b3b transaction             未解除：上下文调查完成，保护依据与 typed pending 口径待决策
RV2c 落选簇编码诊断            已验收：2026-09-30；按第 14.1 节保留裁决
RV2d 不一致簇编码摘要          已验收：2026-09-30；Source ID、长度及 SHA-256
        |
        v
键值合一与证据通道收缩         已完成：2026-10-01；0047、0048，见下文
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

和 RV1 标量的差别必须保住。标量的分簇键仍是规范 JSON，回归由 `TestMigratedQuantityTieUsesCanonicalKey` 守住。`eth_feeHistory` 不得走那条「有编码器就把键换成规范 JSON」的分支。此禁令由 0047 取代：五个方法的键就是规范 JSON，`eth_feeHistory` 在内。Value 仍来自编码器，不从键反序列化再拼对象，这一点未变。

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

状态：已验收（f8b4280）。0043 只覆盖 `eth_getLogs`。比较键仍是现有日志键。收据、交易、access list、区块和 `eth_call` 信封都不在这一刀。

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

### RV2b2 eth_createAccessList

状态：2026-09-30 主检查验收通过。原实现 cade5a3；验收时修正 `normalizeAccessList` 对 `error` 文本误用 hex 大小写折合的问题。比较键仍来自现有 PruneMethod，但其中的 `error` 保留解码后的文本大小写；JSON 转义不改变文本值。显式 null 保持原键，不补成空字符串。收据状态见 RV2b3a；RV3a、RV3b、RV4 仍未解除。

这一刀只做 `eth_createAccessList`。比较键仍是现有的 `normalizeAccessList` / `PruneMethod` 结果，不是另写的整段 JSON。Value 是掩码留下的字段的规范对象。依据是第 4.2 节第一步。不要抄 `projection.StandardFields`，也不要另列一张字段表。

一次提交。先写 0044，再改代码。0044 只采纳这一方法，不改 0041、0042、0043。

受保护字段只限于现有掩码会留下的三个：`accessList`、`error`、`gasUsed`。`accessList` 的每一项只留 `address` 和 `storageKeys`。掩码丢掉的字段不进 Value。`accessList` 或 `storageKeys` 缺失则 EncodeCheck 失败。`error` 和 `gasUsed` 没有时省略，不补 null。空的 `accessList` 或空的 `storageKeys` 仍是空数组，不省略。

顺序沿用第 6.1 节：逐源 EncodeCheck，失败记 Fault，留在分母，不投票；再用现有 access-list 键做唯一一次 Resolve；最后 EncodeCommit。簇内规范字节不同则是 Unknown，`ErrMalformedResponse`，原因含 `result encoding disagrees within cluster`。不第二次投票。

标量、`eth_feeHistory` 和 `eth_getLogs` 的分簇键都不得改。本方法走已有的 `WithCanonicalObject`：有编码器时键仍来自 Normalize。不要把键换成规范 JSON。Value 来自编码器，不从比较键反序列化，也不要解进 `any` 或 `float64`。

规范形式沿用第 5 节：地址和 storage key 小写；`gasUsed` 是规范 QUANTITY，`0x` 加小写十六进制，无前导零，零写作 `0x0`；`error` 是 JSON 字符串，原样保留。数组顺序就是响应顺序，不得排序。对象用编码器 struct 的字段顺序、紧凑 JSON、`SetEscapeHTML(false)`。

result 列只有 `eth_createAccessList` 改为 canonical。Representative 仍是捕获顺序第一人，不决定 Value 拼写。pending 仍是节点本地观测：可以编码被选中的那一份，Representative 保持零值，Status 仍是 Selected。

新文件只允许 `rpc/internal/objects/accesslist/encode.go`。不把通用对象编码器放进 `wire/encode.go`，不建编码器注册表。

测试至少覆盖：同一结果只差大小写或键序时同簇，换序不改变 Value，Representative 跟随第一人；掩码外字段不进 Value，也不把两人拆开；缺 `address` 或 `storageKeys` 记 Fault 且分母不缩小；pending 编码后 Representative 仍为零。标量平票、feeHistory 的 `0.5`/`0.50`、日志的大小写同簇用例仍通过。禁止合成样本，禁止把探针升成 golden，禁止 `-update` 已有三批 expect。

不改：receipt、transaction、blockReceipts、区块重编码、`eth_call` 的 code 与 message、`from` 和 `size`、Extensions、Provenance、分母、门槛。未跟踪的 `cmd/shapeprobe/`、`internal/shapeprobe/`、`internal/rpcsample/probe/` 不读不改。

键外字段这一刀仍无处安放。提交说明写明这个缺口，不要写成已经保留，也不要提前做 Extensions。文档只改现在成为事实的句子。实现执行者不得自行标成已验收；上面的状态由本轮主检查核对后记录。

出口：`go test ./... -count=1`。失败路径是缺 `storageKeys` 的来源留在分母里，不能靠丢掉它变成成功。

### RV2b3a receipt

状态：2026-09-30 主检查验收通过。原实现 2583c28；验收补齐日志 `blockTimestamp` 的规范输出，拒绝 `removed` 缺失/null，避免补成 false。这一刀只编码 `eth_getTransactionReceipt`。交易仍未解除；区块回执数组的独立任务见 RV2b3c。

一次提交。先写 0045，再改代码。0045 只采纳收据，不改 0041、0042、0043、0044。

比较键仍来自现有 `normalizeReceiptObject` / `PruneMethod`，然后去掉 `from`。它不是整段规范 JSON。Value 是受保护字段的规范对象或 JSON null。`from` 按第 14.3 节不进比较键，也不进 Value，所以两人的 `from` 不同仍是同一簇。区块上下文字段这一刀随比较键进入收据 Value，因为它们本来就在单份收据的比较键里；这不解除交易对象的编码。

受保护字段是比较键留下、并且不属于 `from` 的字段：`transactionHash`、`transactionIndex`、`blockHash`、`blockNumber`、`cumulativeGasUsed`、`gasUsed`、`effectiveGasPrice`、`logsBloom`、`logs`、`status` 或 `root`、`type`、`contractAddress`、`to`、`blobGasUsed`、`blobGasPrice`。日志沿用已验收的日志字段，不含 `blockTimestamp` 时省略。`logsBloom` 必须等于 `CreateBloom(logs)`，不相等则 EncodeCheck 失败。

`contractAddress`、`to`、`blobGasUsed`、`blobGasPrice` 没有时省略，不补 null。`contractAddress` 为 JSON null 时省略。空 `logs` 仍是 `[]`。status 与 root 已有的互斥规则保持：两者都没有则失败，同时存在时 status 优先，root 不进 Value。

顺序沿用第 6.1 节：逐源 EncodeCheck，失败记 Fault，留在分母，不投票；再用现有收据键做唯一一次 Resolve；最后 EncodeCommit。簇内规范字节不同则是 Unknown，`ErrMalformedResponse`，原因含 `result encoding disagrees within cluster`。不第二次投票。

result 列只有 `eth_getTransactionReceipt` 改为 canonical。Representative 仍是捕获顺序第一人，不决定 Value 拼写。JSON null 仍是合法的缺失收据，编码为 `null`。

新文件只允许 `rpc/internal/objects/receipt/encode.go`。不建编码器注册表，不改交易和区块回执的 result 列。

测试至少覆盖：同一收据只差大小写或键序时同簇，换序不改变 Value，Representative 跟随第一人；`from` 不进 Value，也不把两人拆开；缺 `logs` 记 Fault 且分母不缩小；`logsBloom` 与 logs 不一致则失败。禁止合成样本，禁止把探针升成 golden，禁止 `-update` 已有三批 expect。

键外字段除已经判定为域外的 `from` 外，这一刀仍无处安放。提交说明写明这个缺口。实现执行者不得自行标成已验收；上面的状态由本轮主检查核对后记录。

出口：`go test ./... -count=1`。失败路径是缺 `logs` 的来源留在分母里，不能靠丢掉它变成成功。

#### RV2b2 / RV2b3a 本轮验收证据

2026-09-30，subagent 执行，主检查复审并两次打回 accessList 文本/null 边界后通过。生产修复仅涉及 accessList 方法 normalizer 与 receipt 编码器；没有改分母、floor、通用 hex 折合或其他方法编码器。

- `TestReturnValueAccessListThroughClient`、`TestReturnValueReceiptFromAndSpellingThroughClient` 走公共 Client、生产 Aggregator 和 typed 读取。accessList pending 仍为 Selected，Representative 为零；receipt 的 `from` 分歧不分簇，不进入 Value。
- `TestReturnValueFaultKeepsClientDenominator` 用四源的两份同值、一份异值、一份坏票，验证两票达不到固定 N=4 的多数门槛。receipt 的缺 logs 在 Admit 失败，accessList 的缺 storageKeys 在 EncodeCheck 失败，均保留分母。
- `TestCanonicalReceiptLogTimestampThroughAdapter`、`TestCanonicalReceiptTimestampDisagreementIsConflict`、`TestCanonicalReceiptRemovedCannotDefaultFromNull` 验证日志字段不会丢失或被填默认值。accessList 的文本大小写、等价 JSON 转义、null 与空串由方法回归测试验证。
- 全部 31 个已跟踪 Go 包测试通过。工作区有三个范围外的未跟踪探针目录，因此本轮全量按 `git ls-files '*.go'` 的目录去重构建包列表，不用 `./...` 扫描它们。可复现命令：`GOCACHE=/tmp/ethguard-return-value-go-cache GOPROXY=off GOTOOLCHAIN=local go test $(git ls-files '*.go' | xargs -n1 dirname | sort -u | sed 's#^#./#') -count=1`。测试只有进程内 HTTP/WSS，无外部网络请求。
- 主检查独立重跑 `go test ./rpc/methods ./ethadapter . -run 'Test(AccessListHex|AccessListNull|CanonicalReceipt|ReturnValue|ReceiptOptional|ReceiptStatus|ReceiptBloom)' -count=1` 通过；`git diff --check` 通过。本轮没有修改 provider record、index、sha256 或 expect。原收据提交 2583c28 的两个 comparison_key 变更是移除 from，全量 replay 通过。
- `BenchmarkReceiptCanonicalValue` 的本机短测量（`-benchmem -benchtime=200ms -count=1`）：0/100/1000 条日志约为 15,658/781,626/8,487,168 B/op，247/10,761/105,308 allocs/op。它是后续数组编码成本的参考，不是资源限额或延迟保证。

解除依据：收据字段与 typed 路径已核对，receipt 与 blockReceipts 的固定 spec 掩码具有相同的收据/日志形状。RV2b3c 依赖收据验收，不依赖交易 inclusion context，因此先于 RV2b3b 开工，仍一次只执行一个任务。

### RV2b3c blockReceipts

状态：2026-09-30 主检查验收通过。决策 0046 只覆盖 `eth_getBlockReceipts`；复用收据编码器，按完整数组投票。0041–0045 的采纳范围不变。

复用 `rpc/internal/objects/receipt/encode.go` 内的收据编码规则，增加数组编码入口，不新增第二份字段模型。比较键仍来自现有 `normalizeBlockReceiptList` / `PruneMethod`，只从每项的键中排除 `from`。不能换成规范数组 JSON 投票。每份来源响应作为完整数组参与现有 Strict 裁决，不逐项跨源拼接。

顶层 JSON null 编成 `null`，空数组保持 `[]`，两者不合并。数组元素不得是 null，顺序不排序。任一元素准入或编码失败，整份来源响应不贡献支持，来源仍占固定分母。日志时间戳出现时保留规范数量，removed 不得由 null/缺失补成 false。请求绑定仍由 `receipt.AdmitBlock` 核对每项的 blockHash 或 blockNumber；该检查不宣称已经证明列表完整，也不增加 receiptsRoot 验证。

沿用已有 WithCanonicalObject：逐源先 EncodeCheck，再用现有方法键分簇，以完整 observations 裁决一次，获胜簇规范字节相等才提交 Value。不同则 Unknown / ErrMalformedResponse，不剔除来源再投票。代表源与 Evidence 同源；Value 不随捕获顺序或 JSON 拼写变化。pending 仍在 Prepare 本地拒绝，不触 anchor 或业务 fan-out。

交付需要普通 Policy 测试及公共 Client / ethadapter 的真实进程内 RPC 测试：大小写、键序和转义同值；from 分歧不分簇且不进 Value；日志时间戳保留；null 的 NotFound 与空数组成功；数组顺序和受保护内容分歧；无效/null 元素排除整份来源且保留分母；请求绑定失败；pending 零请求；获胜簇规范字节不一致失败。更新方法规则索引只能通过其生成测试，diff 应只有 blockReceipts 的 result 变为 canonical。

不修改 transaction、eth_call 信封、区块重编码、Provenance/Extensions、投票分母或门槛。不修改已有样本字节或盲目更新 golden，不读取三个未跟踪探针目录。域外字段仍没有新增保留通道，交付说明继续携带这个缺口。

出口：定向测试、全部已跟踪包测试及 diff 检查通过，记录可复现命令、失败路径、样本/golden 变化和数组编码的分配测量。subagent 不自行标验收；主检查通过后再提交本任务。

#### RV2b3c 本轮验收证据

2026-09-30，subagent 实现，主检查复审并独立运行全部 31 个已跟踪 Go 包，全部通过。全量命令同上一节；`git diff --check` 通过。没有修改 provider record、index、sha256 或 expect。方法规则索引通过生成测试更新，仅 `eth_getBlockReceipts` 的 result 从 raw 改为 canonical。

- 方法与公共 ethadapter 测试覆盖等价拼写、来源换序、from 分歧、日志时间戳、null/空数组、数组顺序和内容分歧、无效/null 元素、请求绑定及 pending 零请求。四源中只有两票有效同值时，坏元素来源仍占分母，结果不能达到三票多数。
- 注入同键但规范字节不同的编码结果，验证返回 Unknown / ErrMalformedResponse，保留原支持者且不重投票。
- 转义测试发现 geth 的 hex 解码器不能直接接受合法的 `"\u0030x..."` 字符串拼写。收据准入现在先解析 JSON 字符串语义，再交给 geth；使用 `UseNumber` 保留整数、精确小数及指数数字，不改来源字节。单笔和数组共用此修复；外来区块绑定及 bloom 错误仍被拒绝。
- 分配测量命令：`GOCACHE=/tmp/ethguard-return-value-go-cache GOPROXY=off GOTOOLCHAIN=local go test ./rpc/methods ./rpc/internal/objects/receipt -run '^$' -bench 'Benchmark(Receipt|BlockReceipts)CanonicalValue' -benchmem -benchtime=200ms -count=1`。

| 输入 | B/op | allocs/op |
| --- | ---: | ---: |
| 单笔收据，0 / 100 / 1000 条日志 | 22,325 / 1,234,209 / 13,499,624 | 327 / 16,460 / 161,443 |
| 数组，0 / 1 / 100 / 1000 笔空日志收据 | 336 / 25,857 / 2,632,080 / 27,589,485 | 7 / 343 / 33,331 / 333,156 |

这是本机短测量，不是资源限额或延迟保证。单笔结果更新了上一节的阶段测量，增加的 JSON 语义解析有可见分配成本；后续优化须保持转义、数字精度和固定分母语义。

### RV2b3 receipt、transaction、blockReceipts

状态：判定完成（2026-09-28）。单笔收据及区块回执数组已按 0045、0046 实现并验收；交易仍未解除。下面记录原始字段判定，已迁移方法的比较键变化以对应采纳决策为准。

判定只走第 4.2 节，不另列字段标准。原始 `PruneMethod` 掩码留下 execution-apis 结果里的字段，所以第一步会把这些字段算进比较键。这不表示它们都被交易根或收据承诺约束。原始掩码含 `from`，但它按第 14.3 节留在域外：交易 hash 约束不了它，没有 CPU 测量就不能定义成派生字段，也不能因为对不上把整票判掉。0045、0046 已从单笔及数组收据的实际比较键移除它。

收据（`eth_getTransactionReceipt`、`eth_getBlockReceipts` 的每一项）：

- 比较键留下：`transactionHash`、`transactionIndex`、`blockHash`、`blockNumber`、`cumulativeGasUsed`、`gasUsed`、`effectiveGasPrice`、`logsBloom`、`logs`、`status`、`root`、`type`、`contractAddress`、`to`、`blobGasUsed`、`blobGasPrice`、`from`。
- 请求绑定：按 hash 取收据时是 `transactionHash`；按区块取收据时是每项的 `blockHash` 或 `blockNumber`。绑定失败已是准入失败。
- 内容承诺：`logsBloom` 必须等于 `CreateBloom(logs)`。不相等已经是整票失败，不是可省略字段。
- 域外：`from`。0045、0046 已从实际比较键及 Value 排除它。其余键外字段仍无处安放，不提前做 Extensions。

交易（`eth_getTransactionByHash`）：

- 比较键留下签名载荷和上下文字段，包括 `hash`、`type`、`nonce`、`gas`、`value`、`input`、`to`、`chainId`、`gasPrice`、`maxFeePerGas`、`maxPriorityFeePerGas`、`accessList`、`authorizationList`、`blobVersionedHashes`、`maxFeePerBlobGas`、`v`、`r`、`s`、`yParity`，以及 `blockHash`、`blockNumber`、`blockTimestamp`、`transactionIndex`、`from`。
- 已验证承诺：`hash` 必须等于本地 `tx.Hash()`。不相等已经排除整票。`tx.Hash()` 不覆盖 `from`、`blockHash`、`blockNumber`、`blockTimestamp`、`transactionIndex`。
- 域外：`from`。区块上下文字段要等区块承诺核对之后才能进交易 Value，这一步还没有。

单笔收据按 0045 把区块上下文字段随比较键纳入 Value，并从键与 Value 排除 from；不能再把它描述成仍不编码或上下文全部域外。区块回执数组沿用这套字段规则，另核对数组的请求绑定。交易的区块上下文仍待核对：当前 `DecodeTransaction` 由 blockNumber 是否存在返回 pending，不能简单删掉上下文后把 mined 交易改成 pending。RV2b3b 需要先写明上下文的保护依据及 typed pending 判定，再解除编码。Extensions 差集、Provenance 和 RV3 的任务出口仍需后续单独细化。

#### RV2b3b 解除调查（2026-09-30）

主检查核对 subagent 的代码调查，结论是继续保持未解除：第 4.2 节第一步允许比较键中的字段进域，但上面的交易任务又要求区块上下文先核对承诺。这两条不能由编码器暗中选择。现有 `transaction.Validate` 只核对签名载荷 hash、上下文格式，以及 blockHash 有值时 number/index 必须存在；没有查询区块或验证交易 inclusion。不能把现有上下文校验写成承诺验证。

`DecodeTransaction` 按 blockNumber 非缺失且非 null 返回 pending=false；它不要求 blockHash 必须存在。`TestAdapterTransactionByHashReportsPendingFlag` 的 mined 输入只有 blockNumber，因此迁移不能顺手收紧为三个上下文字段全有。直接使用 geth 的 `Transaction.MarshalJSON` 会丢掉上下文，使 mined 被误报为 pending，也不能保证保留方法掩码中的所有字段及其可选性。

后续需选择并记录统一口径：沿用比较键佐证上下文，明确它不是 inclusion proof；或坚持区块承诺要求，另设计所需验证和 typed pending 输入。前者需要采纳决策修订本任务，后者需要独立接口与准入设计。两条均未在本轮实现。`from` 默认域外、固定分母和一次裁决不受此选择影响。

独立的已锁定缺口可以先推进：第 14.1 节要求落选簇规范字节不一致写入 Faults，调查时 Strict 只检查排名第一簇。该诊断不依赖交易上下文设计；实施范围和出口在下面的 RV2c。

### RV2c 落选簇编码诊断

状态：2026-09-30 主检查验收通过。依据是 `return-value-design.md` 第 14.1 节已锁定的做法乙，不新增采纳决策。本任务独立于未解除的交易上下文设计，补齐既有编码管线的诊断缺口。

普通 Strict 挂编码器的路径在唯一一次裁决后检查排名第一簇及落选簇。落选簇规范字节不一致只追加 Faults，诊断包含该簇的 Source ID，不携带完整比较键。不改 Status、返回错误、Value、Support、Reject、Representative 或分母。获胜簇自身不一致仍 Unknown / ErrMalformedResponse，不再投票。检查落选字节只比较，不复制一份不会返回的大 Value。Resolve、ResolveCorroborated、legacy 和 unanimity 都走同一提交收尾。

必要证据：五源三票同键同编码的胜者与两票同键不同编码的落选簇，仍 Corroborated 且记录诊断；floor 4 保持普通 ErrConflict，不能凭坏票减分母；获胜簇自身不一致仍失败；legacy/unanimity 保留原冲突语义；一致的落选簇不添 Fault；真实 Aggregator 以进程内 RPC 保留五人 Participants、三人 Support、两人 Reject，并在 floor 3/4 下保持上述结果。每源编码一次，不第二次裁决。

不修改 eth_call revert resolver、transaction、Provenance/Extensions 或 provider 样本/golden。同步实现事实，保留尚未实现的编码摘要要求。出口：定向测试、全部已跟踪包测试、diff 检查通过。执行者不自标验收或提交，由主检查验收后记录提交。

验收证据：subagent 实现，主检查独立运行全部 31 个已跟踪 Go 包测试（命令同 RV2b2 / RV2b3a 验收节），全部通过；`git diff --check` 通过。`TestStrictRejectedEncodingDiagnosticPreservesAdjudication` 覆盖两条 Resolve 入口、majority/unanimity 和 floor 3/4；legacy、胜者自身错误及一致落选簇另有回归测试。`TestAggregateRejectedEncodingDoesNotChangeVotingBasis` 走五节点进程内 RPC，验证成功及未达 floor 两条路径。provider 样本/golden 未变。

### RV2d 不一致簇编码摘要

状态：2026-09-30 主检查验收通过。RV2c 已通过主检查，本批按 `return-value-design.md` 第 6.4 节补齐不一致簇内各来源的编码摘要。

只在簇规范字节不一致时生成诊断，为每个来源记录 Source ID、规范编码字节长度及 SHA-256，随该簇错误写入 Faults。包括排名第一簇与落选簇；不保存完整比较键或 Value，不重编码来源，也不新增公开证据类型或预算。获胜簇错误的返回分类、落选簇仅诊断、固定分母及一次裁决保持 RV2c 的行为。

出口：测试核对摘要与实际规范字节对应、等值字节的摘要相同、不等值字节可区分、来源顺序可关联；一致簇不添诊断；原有五源裁决矩阵及真实 Aggregator 测试继续通过。主检查独立运行全部已跟踪包测试和 diff 检查后验收并本地提交，不改样本/golden。

验收证据：主检查复审 subagent 实现，独立运行全部 31 个已跟踪 Go 包测试（命令同 RV2b2 / RV2b3a 验收节），全部通过；`git diff --check` 通过。`TestStrictInconsistentClustersRecordActualEncodingDigests` 在两条 Resolve 入口及 legacy 下核对胜者/落选簇的逐源摘要，包含等值字节、不同长度字节与带引号的 Source ID；`TestAggregateRejectedEncodingDoesNotChangeVotingBasis` 从真实五节点 RPC 检查摘要和 floor 3/4 结果。摘要不携带完整键或 Value，不增加每源编码次数。provider 样本/golden 未变。

本轮已明确的编码管线诊断缺口已完成。后续 transaction 仍先统一上下文保护依据与 typed pending 口径；Provenance/Extensions 和 RV3 的接口及出口另行细化，不因 RV2c/RV2d 验收自动解除。

### 键值合一与证据通道收缩（2026-10-01）

状态：已完成。决策 0047、0048。

| 提交 | 内容 |
| --- | --- |
| 836df66、27631e6 | 收据编码器从自己的规范日志计算 bloom 与逐日志绑定，不再重复调用 `Validate`；保留零哈希容忍 |
| 89786a8 | 删掉收据键路径的第二遍加工，键字节不变 |
| 574bfd9 | 0047：五个对象方法换到 `WithCanonicalValue`，比较键即规范字节；`strict.go` 的键选择不动 |
| f6fe088、5a529d2 | 方法规则索引新增「键来源」列；Strict 总是声明（`canonical-bytes` / `normalizer`） |
| 0502708 | 接受域包含测试与 `BenchmarkStrictResolveReceipt` |
| 7967e7b | 接入测试套件 `rpc/methods/admission_suite_test.go`：每个方法一行 |
| 5a0f53b | 证据通道不保留代表源 Raw，撤销第 14.6 节；决策记录为 0048 |

要点：

- 守卫保留。`finish()` 的簇内字节检查对键即字节的方法恒真，对刻意保留窄键的方法承重；删掉它，窄键回归会静默产出 Corroborated。`median.go` 的同类检查依赖「编码器只由数值决定」，同样保留。
- 「键来源」列是评审摩擦级护栏，不保证接线正确（0047 Unresolved）。
- 成本结构：一次收据调用曾对同一份字节解析约九遍（jsonsafe、准入、EncodeCheck、比较键两遍、`AggregateFields`、Extensions 指纹、typed 解码）。本轮去掉了重复校验与第二遍键加工，键值合一又让聚类不再调用 normalizer。仍在的是 `AggregateFields`、Extensions 指纹对规范 Value 的再规范化、`usableAnswer` 对 normalizer 的调用，Step 4–6 会再碰到它们。
- `PruneMethod` 与 spec 掩码仍不能删：已迁移方法的 normalizer 仍服务 `usableAnswer`、anchor 与 pending。

### 后续规划

按方法原型推进，但每个原型都必须以聚合结论为出口。键即字节的对象原型
已定型，新方法加接入套件一行即可；其余三种原型各先做透一个代表方法。
不能因为对象难以完整重编码，就回退为代表源原文或“最可靠来源”的值。

| 原型 | 代表方法 | 卡在哪里 |
| --- | --- | --- |
| 键即字节的对象 | 已迁移的五个方法 | 已定型 |
| 窄键对象 | 区块（RV3b） | 承诺核对；必须经真实区块 policy 证明窄键与守卫（0047 Unresolved），并证明 Value 仍是聚合产物 |
| 准入绑定 | `eth_getTransactionByHash`（RV2b3b） | 上下文保护依据与 typed pending 口径，先做决策；不能以代表交易替代 inclusion 结论 |
| 多维键 | `eth_call`（RV3a） | revert 信封与多维键；附带 0048 的 API 破坏；各维度必须聚合或明确弃权 |

顺序沿用 2026-09-12 的选择，两条线之间没有代码依赖：

1. **写路径契约固化**（decision-map #4）。已完成：决策 0049，`WriteQuorum` / `ResolveWriteBroadcast` 状态矩阵，forwarded 与本地 hash fail-closed 测试，以及通过 `rpctest` 进程内节点的广播端到端测试。协议拒绝保留为 `Rejected`，不把 “already known” 等错误文本升级为接受；回执等待和交易生命周期仍在调用方。
2. **WSS 事件锚点**（#5）。写路径出口已解除。
3. **读路径原型**：RV3b 区块 → RV2b3b 交易 → Step 4（删 `projection.StandardFields`，前置是区块与交易重编码）→ `eth_call` 一族（RV3a、多维键合流、0048 的 `RevertError` 与 `Evidence` 改名，作为一次记录在案的 API 破坏）→ Step 6（准入、EncodeCheck、normalize 等逐源闸门合成一段，最后做）→ RV4（已注册方法不再有 `raw`）。每一步都以“聚合结论或明确 Unknown/Conflict”为出口，不允许把单源 Selected/Forwarded 伪装成已聚合值。
4. **批量扩张只读方法**：每个方法套用对应原型与接入套件。

后续每个方法按同一条验收路线推进：先定义哪些字段能够进入聚合域，
再定义缺失、分歧和无法编码时的弃权规则；随后让所有候选来源经过同一套
请求绑定、准入和编码，进行一次固定分母的裁决；最后只从裁决产物做 typed
projection，并用真实 `Aggregator` / adapter 测试验证成功、冲突、无效、
pending 和 provider fault。任何只在 Policy 层成立、或靠替换为单源值才变绿
的实现都不能解除下一阶段。

开放项，不挡住上面任何一步：`from` 的测量后归属；`median.go` 的 `equal` 是否改为按键筛选；接入套件的形状变体能否从 `internal/spec/methods.json` 生成，代替手工补语料；是否存在经 `replace` / `go.work` 引用本库的外部使用者（决定 API 破坏要不要弃用窗口）。

## T07：标准驱动的自动兼容闭环（下一阶段主线）

状态：进行中（T07.0–T07.3、T07.5 已完成；T07.4 收口中）。T07 是返回值框架之后的下一项主线工作；它把官方
`execution-apis`、现有多源聚合器和真实 RPC 形态探查接成一个长期可运行的
兼容性闭环。目标不是让使用方配置 quorum、拜占庭模型或字段策略，而是由
客户端在每次调用中根据固定来源集合动态计算接受门槛，并向下层隐藏多源细节。

### T07.0 先冻结简化后的结果模型

依赖：当前返回值框架的 Core 投影出口。状态：已冻结（2026-10-11，决策
0050）；实现迁移仍由 T07.1–T07.2 完成。出口：新增 ADR 或修订现有架构记录，
删除相互重叠的字段保护、来源分类和公开裁决概念。

- **Core** 只包含当前锁定版本的 `execution-apis` result schema 中、适用于该
  方法和分叉的官方字段。未知字段不得因为出现而进入 Core；手写方法逻辑只
  能补充请求绑定、分叉适用性和完整性校验，不能自行扩大受保护字段集合。
- **Extension** 是 Core 之外的响应字段。它们不参与普通结果的核心比较，也不
  因为多个来源同时返回就被宣称为标准字段。
- **Diagnostics** 保存扩展字段的观测和来源证据。普通 RPC/typed 表面不返回
  多源列表、Support/Reject/Abstain 维度或 provider 原文；高级诊断入口才可
  查看这些信息。
- Extension 只保留一个内部接受判定：同一路径、同一规范值的支持数达到本次
  调用的动态门槛时，才允许作为可选扩展结果；否则只留在 Diagnostics。全部、
  部分或单一来源返回只是支持数与固定分母的计算结果，不再成为对外概念。
- `Corroborated` 继续表示 provider-backed cross-source evidence。T07 不引入
  Ethereum consensus、BFT 或链上最终性的对外声明。

### T07.1 把接受门槛收回客户端内部

依赖：T07.0、现有 `Aggregator` / `Corroboration.Required`。状态：已完成
（2026-10-11）；字段观察聚合已接收固定调用快照，固定分母门槛已集中到内部
计算器，旧的用户配置入口已直接移除。出口：
使用方只提供来源和连接配置；quorum、corroboration mode、拜占庭参数和字段
接受门槛不出现在公开配置或普通构造路径中。

- 调用开始时固定本次 voting basis；失败、超时和未发出的成员仍占分母。
- 由内部 acceptance calculator 根据固定 basis、方法类别和调用形态计算 `Q(N)`。
  现有的动态 `Required(n)`、单源不晋升为 Corroborated、pending/local
  observation 分流作为基线，不复制第二套 resolver。
- Core 字段沿用方法级准入和现有 Strict/Median/Local 语义；Extension 值只在
  `k >= Q(N)` 且 `k` 个来源的规范值相同的时候可接受。
- N 不足以形成多源佐证时，普通表面返回已有明确的 Selected/Forwarded/错误
  语义，不把单源结果伪装成聚合结论。
- 直接删除根客户端的 `ethguard.WithQuorum`、`WithCorroborationMode`、
  `WithForwardingMode`、`SetCorroboration` 以及配置文件中的 `quorum` /
  `corroboration` 字段；低层 `rpc` policy 构造器仍只供库内部方法组装和测试，
  不进入根客户端配置。测试、示例和文档同步改为库内部动态策略。项目尚未
  对外发布，不设置弃用窗口。

### T07.2 建立 Core / Extension 单一评估模块

依赖：T07.0、T07.1。状态：已完成（2026-10-11）。generic field aggregation
已重塑为 `Evaluation{Accepted, Diagnostics}`，投影只消费已接受字段；
`Decision.Extensions` 只携带达到门槛的扩展，其他字段进入
`Decision.Diagnostics`。旧的 Common/Residuals/Conflicts 对外 observation view
已移除。出口：Aggregator 通过一个内部 seam 得到统一评估结果，方法包不再分别
维护多套扩展字段分类逻辑。

建议的内部结果形状：

```text
Evaluation {
    CoreValue
    CoreStatus
    AcceptedExtensions
    Diagnostics
}
```

- 输入是方法、请求、标准版本和原始多源 observations。
- 输出只有一个 Core 结论；AcceptedExtensions 是可选、受门槛保护的附加值；
  Diagnostics 不改变 CoreStatus。
- 未知字段、规范版本之外的字段、厂商字段和新客户端字段统一走 Extension，
  不再分别建立 `raw/canonical`、`provenance`、`withheld` 等互相重叠的公开域。
- 现有 `Decision.Value` 继续是下层唯一业务值；扩展证据通过既有诊断/Uncertain
  通道提供，不新增第二套 quorum、evidence 或 resolver 类型。

### T07.3 Chainlist 多 RPC 形态探查

依赖：T07.0、T07.2；允许复用当前 `internal/chainlist`、`internal/shapeprobe`
和 `cmd/shapeprobe`。状态：已完成（2026-10-11）。出口：可重复生成脱敏的
provider-shape 批次和 Core/Extension 差异报告，但不直接修改生产策略或标准数据。

- 从 Chainlist 发现候选端点，去重并做 `chainId`、`eth_blockNumber` 等最小预检；
  Chainlist 只提供发现来源，不提供独立性、信誉或共识成员资格。
- 按方法、合法请求形态、固定区块高度和适用分叉采集原始 JSON-RPC 响应；记录
  chain、method、request、source label、HTTP/WSS transport、client version、
  observation time、spec version 和原始字节 hash。
- 采集结果先走统一 JSON 安全检查、Execution API Core schema 投影和现有方法
  normalizer，再把未知字段分流到 Extension；不把探查结果直接写成 quorum golden。
- 报告现在复用统一 `Evaluation` seam：按固定调用分母输出 Core、达到门槛的
  Extension、residual/conflict，以及现有 normalizer 的成功/失败、值分歧和
  transport/provider fault。报告是观察和回归输入，不替代方法契约。
- 对同一锚点的重复采样必须可重放；外部网络采集和离线 replay 分开，CI 只消费
  已脱敏、带 hash 的批次。

### T07.4 官方标准升级闭环

依赖：T07.2、T07.3。状态：进行中（2026-10-11，官方 schema、projector 和
canonical encoder 覆盖门禁已完成；待目标环境重跑完整批次后关闭）。
出口：上游 `execution-apis` release 变化能产生可审阅 diff、触发兼容测试，并将
疑似标准滞后字段报告出来。

- 升级只从官方固定 release 进入，不从 provider 反馈自动生成或提升 Core 字段。
- 自动重建完整方法 schema 或当前支持方法的投影、字段 mask 和版本互锁；变更
  必须显示新增、删除、类型和 required 变化。当前已提供
  `cmd/specbundle`，从官方 dereferenced `openrpc.json` 按
  `internal/spec/methods.list` 重建 pinned bundle；生成物不再承担方法选择职责。
- 官方 schema 新增字段时，先进入标准 diff 和 Extension/Core 迁移检查；没有
  通过准入、请求绑定、编码和回放测试前，不自动改变生产返回值。
- 形态报告的 Core 路径现在从 pinned result schema 派生；本轮已把
  `eth_feeHistory` 的 `baseFeePerBlobGas`、`blobGasUsedRatio` 贯通到准入、规范
  编码和投影，作为新增官方字段迁移样例。
- 官方 schema 删除或改变字段时，CI 阻止静默继续使用旧保护规则；需明确迁移、
  兼容窗口和 golden 变化。
- `internal/spec/vectors` 全部接入离线 conformance harness。官方向量验证 wire
  形状；`eth_sendRawTransaction` 的 32 字节交易哈希 result 已纳入 pinned bundle，
  向量不再留在 Uncovered；真实 Chainlist 批次验证 provider 变体；两者不互相替代。
- `rpc/projection` 的 coverage gate 把所有生产 projector 与 pinned schema 的
  JSON 路径逐项比较，新增官方字段若没有 projector 声明会直接使 CI 失败；
  `eth_getBlockReceipts` 复用 receipt projector。receipt、log、feeHistory 和
  access-list 的 canonical encoder 也通过同一条路径覆盖门禁；receipt 的官方
  `from` 已进入 canonical value。

### T07.5 端到端验收与默认化

依赖：T07.1–T07.4。状态：已完成（2026-10-11；全量 `go test ./...` 已在目标环境通过）。
出口：普通调用者无需配置多源共识参数，且
完整生产路径的 Core/Extension 行为可由批次重放和进程内 RPC 复现。

- Policy、Aggregator、typed projection 和 adapter 都必须走同一套 Core 评估结果；
  只通过 Policy 层的测试不能解除任务。
- 覆盖固定 basis、失败占分母、N=1/2/3/4/5、核心值冲突、扩展值达到门槛、
  扩展值低于门槛、扩展值分歧、标准版本升级和未知字段注入。
- 未知字段不制造 Core 冲突；核心字段不满足门槛时返回既有 Unknown/Conflict，
  不降低门槛、不缩小分母、不改走代表源。
- 集成验收覆盖默认动态 majority 的少数分歧、低支持 Extension 进入
  Diagnostics、达到门槛 Extension 进入 Decision.Extensions，以及 typed adapter
  只返回单一业务值的边界；测试不再依赖调用者设置 unanimity/quorum 模式。
- typed/ethclient 表面只看到单一结果或错误；来源比例、字段路径和原始响应只
  在显式诊断路径可见。
- 验收材料包括离线 replay、进程内端到端测试、schema diff、扩展字段报告和
  `go test ./...`；禁止以外部实时 RPC 的一次成功代替可重放证据。

T07 的非目标：不实现轻客户端、BLS、sync committee、provider 权重、failure
domain 模型或新的公开 quorum 模式；不根据多 RPC 反馈自动修改官方标准；不把
provider-backed evidence 写成链上共识或链上最终性。

```text
T07.0  Core / Extension / Diagnostics 简化模型
        |
        v
T07.1  内部门槛动态计算，移除用户共识配置负担
        |
        v
T07.2  单一 Core/Extension 评估 seam
        |
        +--> T07.3  Chainlist 多 RPC 形态探查与离线报告
        |
        +--> T07.4  官方 execution-apis 升级与 conformance 闭环
                       |
                       v
                 T07.5  默认化与端到端验收（已完成）
```

### T07 下一步：收口 T07.4 官方标准升级闭环

T07.5 已关闭后，下一切片回到 T07.4 的剩余出口，目标是让标准升级在 CI 中可审阅、
可阻断、可回放：

1. ~~从官方 `execution-apis` release 重新核对 `eth_sendRawTransaction` 的 result
   schema，决定将其纳入 pinned bundle，或把它记录为明确的非 bundle 向量；不得用
   provider 样本替代官方 schema。~~ 已完成：官方 schema 通过生成边界纳入
   `methods.json`，方法选择由 `methods.list` 明确记录。
2. ~~为 `cmd/specdiff` 增加 CI gate：候选 bundle 出现方法、字段、类型或 required
   变化而没有对应迁移记录和 conformance/replay 结果时，CI 失败。~~ 已完成：
   `-check -migration` 精确校验 diff 与 manifest，并要求两类验证证据。
3. ~~将标准字段迁移检查从 projector 扩展到所有当前手写 canonical encoder，确保官方
   新增字段不会只在 shape report 中变成 Core，却在生产 `Decision.Value` 中静默丢失。~~
   已完成：projector 与 receipt/log/feeHistory/access-list encoder 统一由 pinned
   schema 路径门禁检查，receipt 官方 `from` 不再被静默删除。
4. 重新运行 pinned vectors、Chainlist 离线批次和全量测试；通过后关闭 T07.4，
   再进入 T08 的 revert envelope 与多维比较键工作。

## 排后：未捕获读方法的捕获批次

状态：排在 T07 标准驱动闭环之后。2026-09-25 的「未采集」已过时：`internal/rpcsample/probe/mainnet/2026-09-26/` 有一次形状探查，报告只陈述观察，不判断如何处理。它不是 `testdata` 批次，没有 `expect.json`，不解除 `eth_chainId`，也不改变 `TestRegisteredMethodSampleStatus` 钉住的名单。返回值和 T07 任务不得把这批探针直接升成 golden，不得合成样本。

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
6. 对外说明它提供的是 corroboration evidence，而非 Ethereum consensus proof；
7. 进入规范编码的方法在 `rpc/methods/admission_suite_test.go` 加一行，并在方法规则索引声明键来源。

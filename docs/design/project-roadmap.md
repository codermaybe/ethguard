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
3. EIP-1898 object 形态样本已收录在 `internal/rpcsample/testdata/eip1898-object`。端点已脱敏。离线测试只重放形状，不把样本写成兼容性结论。
4. `eth_estimateGas`、`eth_createAccessList` 的对象形态已与 T02 五个 state-read 对齐：移动标签对象改写为 `{blockHash}`，显式对象和 pending 透传，非法对象在 anchor 之前本地拒绝。
5. `eth_getLogs` 的 hash / pending / 范围字段保持 T03-A：pending 本地拒绝，`blockHash` 与范围字段互斥。没有改成已支持 pending。
6. `method-contracts.md`、moving-tag matrix 与上述代码一致。matrix 的可执行来源仍是测试；文档只作对照。

## T04：内容与错误兼容性

1. revert data 比较键已区分缺失、null、空字节、非空 hex 与非 hex JSON。样本 `internal/rpcsample/testdata/revert-data`。测试：`TestNormalizeCallOutcomeSeparatesRevertDataStates`、`TestCallRevertDataConflictIsNotSwallowed`、`TestRevertSamplesUseDistinctDataKeys`、`TestRevertDataSamplesReplayWithoutEndpoints`。
2. block / receipt / transaction 的真实形态在 `internal/rpcsample/testdata/block-receipt-tx`。白名单外字段不制造分歧，缺必填字段不进入准入，另一块内容绑定失败。测试：`TestRealShapesAgreeAfterPruneAndCaseFold`、`TestMissingRequiredFieldsStayOutOfAdmission`、`TestTypedRealHeaderRejectsAnotherBlocksContent`。
3. wire、admission、content check 与 typed projection 分开。空 uncle 列表和空 receipt 数组保持合法。测试：`TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected`、`TestAdmitDoesNotChangeComparisonKey`、`TestWireNormalizeDoesNotRejectUnknownFields`、`TestTypedReceiptFromAnotherHashIsMalformed`。
4. 同值响应仍只写成 provider-backed evidence。`TestMethodContractsForbiddenClaims` 与 matrix 的 forbiddenClaims 互锁。

## T05：运行期证据与可观测性

1. 已完成。provider-backed reference 的新鲜度和失败语义已写成契约：本次调用内建立，无复用、无 TTL（决策 0026）；`ObservedAt` 是本次建立时刻；失败是 Prepare 期错误，包裹 anchor 原因，业务零 fan-out；证据 `Status=Unknown` 且保留 `Faults`。测试：`TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout`、`TestReferenceEstablishedFreshPerCall`、`TestReferenceFailureEvidenceIsUnknownWithObservedAt`。
2. 已完成。`DecisionEvent` 与 `rpc.Status` 保持双模型，没有把 `failed` 加进置信状态。成功事件串等于 `Status.String()`；conflict、no-valid-response、invalid-request 的事件都是 `failed`，`Conflict` 当且仅当 `ErrConflict`；`Status.String()` 的输出集合恰为 `unknown`、`corroborated`、`selected`、`forwarded`。测试：`TestDecisionEventStatusMatchesSuccessClass`、`TestDecisionEventFailedStatusForClassifiedErrors`、`TestStatusStringSetExcludesFailed`。
3. 已完成。`corroborated` label 迁移记录在 `observe/prometheus` 包注释：status 四取值、告警看 `conflicts_total` 与 `status="failed"`、method/source 基数界。测试：`TestDocumentedDecisionStatusLabelsMatchEmissions`。
4. 已完成。不新增通用 evidence validity helper，见决策 0038。裸状态比较与 `IsCorroborated(err)` 的分工已写明；被拒选项是新增结构化 helper 并迁移全部内部判断点。

## T06：测试流水线收敛与方法合规（计划中）

标准见 `method-verification-pipeline.md`；本节只记录实施顺序、依赖和出口。
静态索引可以立即开始，完整 replay / golden 在 WSS anchor 的参考语义稳定后进入主线；读方法批次扩展必须等 T06 核心出口通过。

### T06.0 冻结设计约束

依赖：无。出口：设计记录和标准互相引用，后续任务不再重新讨论这些边界。

- 0039 规则 4e 采用方案 (i)：返回已佐证的 revert 行为，未佐证的 data 通过删减式投影扣除，只在 Uncertain / 审计证据中保留原始字节。
- 明确 dimension evidence 至少能区分每个维度的 `Support`、`Reject` 和 `Abstain`，golden 同步记录删减字段。
- 修订 `Decision.Value` 的不变量：允许方法契约声明的删减式投影；禁止新增、替换或跨来源拼装字段。
- 保持 Policy 组合和真实测试为执行事实；不引入 `MethodContract` DSL、代码生成或通用 `suite.RunAll`（决策 0040）。
- 修正 T06 旧描述：`rpc.RPCError` 已有 `Data` 字段，缺口在 `rpctest` 的原样 error-body 返回和转换路径对缺失 / `null` 的保留。

### T06.1 静态互锁

依赖：T06.0。出口：CI 能在 matrix 行与 RegisteredMethod 双向检查，并能确认 `Tests` 列中的每个完整函数名真实存在。

- 用 `go/parser` 扫描仓库内 `func Test*`。
- 只检查 `moving-tag-matrix.md` 的 `Tests` 列；`method-contracts.md` 与 roadmap 中的测试名保留为说明性引用。
- 只接受完整测试函数名。清理 matrix 现有的 `Prefix*` 和 `as above`，不再扩展 Markdown 解析语法。
- 维持现有的列数、非空单元格、catalog 双向覆盖和 forbidden-claims 检查。

### T06.2 Replay 基础设施

依赖：T06.0；端到端 golden 依赖 WSS anchor 的参考语义稳定。出口：同一批样本可以通过 Policy 层和 Aggregator 层重放。

- 提取 `internal/rpcsample/replay`，合并现有样本加载逻辑，保留每条 record 的 sha256 校验。
- Policy 层用真实 Policy 构造 `rpc.Observation`，错误响应保留完整 `RPCError` data 形态。
- 端到端层经 `rpctest` 原样返回记录的 JSON-RPC 响应体，走 `Aggregator.Aggregate`，覆盖 Admit 和 typed-read 门禁（0038）。
- 为 `latest` 请求提供进程内 anchor 应答；禁止外部网络 I/O，允许 `httptest`。
- `rpctest` 增加缺失 data、`null`、`0x` 和具体 data 的 error-body 表达，避免依靠错误字符串重建响应。

### T06.3 Golden 快照与批次归档

依赖：T06.2。出口：CI 能从批次目录重放并比较 golden，变更必须产生可审阅 diff。

- 按 `testdata/<feature-or-method>/<batch-id>/` 组织批次；每批次保留 `index.json`，不删除 sha256 凭据。
- `expect.json` 固定 schema 版本、request、mode、floor、成员数 N、每源比较键、准入 Faults、维度证据、删减字段和最终 Decision / error。
- golden 是数据格式，不包含条件、继承、通配或生成测试逻辑；禁止盲目 `-update`。
- 采集时尽力记录 `web3_clientVersion`，只作上下文，不能用于 provider / client 归因。
- 现有三个样本目录迁移到批次布局时保持 record 内容和 record sha256 不变。

### T06.4 决策 0039 落地

依赖：T06.0、T06.2；出口：4e 的所有代表性矩阵和端到端调用通过，文档不再描述旧的扁平键结果。

- 在 `eth_call` 上声明 revert `data` 可弃权，由方法专属多维 resolver 处理；generic Strict 保持单 key 语义。
- 弃权源留在投票分母，不进 data 的 Reject，不用其他来源的值补全。
- 短 data 簇采用删减式投影；typed `CallContract` 返回无 data 的 corroborated revert，原始具体 data 仅经 Uncertain / 审计证据可见。
- 覆盖 N=2、N=3、N=4、全 abstain、具体 data 达到 quorum、具体 data 冲突和 unanimity 模式。
- 更新 `method-contracts.md` 0.5、matrix 的 eth_call 行、相关现状测试及 golden。

### T06.5 扩展读方法批次

依赖：T06.1–T06.4 及 WSS anchor 语义稳定。出口：每个新增 RegisteredMethod 通过标准第 5 节清单，并提交对应样本状态和 golden。

- 按方法逐项增加 matrix、Policy 测试、admission / normalization 边界、端到端 replay 和 golden。
- 不为批量扩展新增声明式方法契约或统一生成器；重复行为通过普通 Go helper 和 table-driven tests 复用。

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
